//go:build testbed

package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/ntpserver"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The level 1 tests of the NTP server and the time faults (plan M34, the list M34_LEVEL1), on the real kernel: a client of a test
// network asks the gateway address or a server of the uplink for the time, the redirect (D36: a port per destination selector)
// brings the query to the server in the service namespace, and the server answers as the time faults of the device say. Each test
// has its own gateway. The exact assertions are made with sntp.py, the cases about a real client with `chronyd -Q` and `ntpdig`.
// Neither sets the clock.

const (
	ntpGateway = testbed.LAN0Gateway + ":123"
	fault400d  = 400 * 24 * time.Hour
)

// clockTolerance is how far the served time may be from the host clock for "the correct time": 50 ms where the timing is
// meaningful (plan M34), a second under software emulation.
func clockTolerance() time.Duration {
	if testbed.Accurate() {
		return 50 * time.Millisecond
	}
	return time.Second
}

// deviceState is what the API counts for a device (its id): the queries and the answers each fault shaped.
func (b *ntpBed) deviceState(t *testing.T, id string) (queries int64, answers map[string]int64, ok bool) {
	t.Helper()
	d, found := b.g.srv.NTPDeviceState(id)
	return d.Queries, d.Answers, found
}

// M34 level 1: a client of a test network gets the host's time from the server: within 50 ms of the host clock, at stratum 2, with
// leap indicator 0 and the gateway's address in the network as reference id, from the address and port it asked, in a reply
// of 48 bytes, in the version of the query. The real clients take it: chrony measures an offset of the same size, ntpdig prints
// stratum 2 and no leap.
func TestAnSNTPClientIsAnsweredWithinFiftyMillisecondsAtStratum2AndChronyAndNtpdigTakeItOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t)
	a := b.top.A
	b.until(t, "the gateway address answers A", a, ntpGateway, func(r sntpReply) bool { return r.Replied })
	r := b.best(t, a, ntpGateway, 5)
	if !r.StrictOK || r.Mode != 4 || r.Version != 4 || r.Leap != 0 || r.Stratum != 2 || r.Size != 48 {
		t.Errorf("the answer of a correct server: %+v", r)
	}
	if r.From != testbed.LAN0Gateway || r.FromPort != 123 || r.RefIDIP != testbed.LAN0Gateway {
		t.Errorf("the answer came from %s:%d with the reference id %s, want the gateway address %s:123 twice", r.From, r.FromPort, r.RefIDIP, testbed.LAN0Gateway)
	}
	if d := r.shift(); abs(d) > clockTolerance() {
		t.Errorf("the served time is %v off the host clock, want within %v", d, clockTolerance())
	}
	if r.Precision >= 0 || r.RootDispersion <= 0 || r.RootDispersion > 1 {
		t.Errorf("precision %d and root dispersion %v of an answer that comes from the kernel's error", r.Precision, r.RootDispersion)
	}
	if r.Reference == nil || r.Receive == nil || r.Origin == nil || !r.OriginOK {
		t.Errorf("a timestamp is missing or the origin is not the query's: %+v", r)
	}
	// NTPv3 clients get a version 3 reply
	if v3 := b.ask(t, a, ntpGateway, "--version", "3"); !v3.Replied || v3.Version != 3 || v3.Mode != 4 || v3.Stratum != 2 {
		t.Errorf("the answer to a version 3 query: %+v", v3)
	}

	if res := b.chronyQ(t, a, testbed.LAN0Gateway); !res.Took || abs(res.Offset) > clockTolerance() {
		t.Errorf("chrony took the server and measured %v (took %v), want an offset within %v:\n%s", res.Offset, res.Took, clockTolerance(), res.Output)
	}
	if res := b.ntpdig(t, a, testbed.LAN0Gateway); !res.Took || res.Stratum != 2 || res.Leap != "no-leap" || abs(res.Offset) > clockTolerance() {
		t.Errorf("ntpdig: %+v", res)
	}
}

// M34 level 1: a client that asks a server of the uplink (hardcoded in its firmware) gets the answer of that server while the
// network does not redirect, and the answer of the gateway, from the address and port of that server, once it does. The server of
// the uplink is a stand-in that labels its answers and logs its queries.
func TestAClientAskingAHardcodedServerGetsTheGatewaysAnswerFromThatServersAddressOnceRedirectedAndTheRealOneOtherwiseOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t)
	g, a := b.g, b.top.A
	real := b.startFakeNTP(t, b.top.Server, testbed.ServerAddr, "REAL")
	server := testbed.ServerAddr + ":123"

	// not redirected: the real server answers, the gateway address is the gateway's
	r := b.ask(t, a, server)
	if !r.Replied || r.RefIDASCII != "REAL" || r.Stratum != 3 || r.From != testbed.ServerAddr {
		t.Fatalf("the answer of the real server: %+v", r)
	}
	if n := queriesIn(real); n != 1 {
		t.Errorf("the real server saw %d queries, want 1", n)
	}
	if gw := b.ask(t, a, ntpGateway); !gw.Replied || gw.Stratum != 2 || gw.RefIDIP != testbed.LAN0Gateway {
		t.Errorf("the gateway address: %+v", gw)
	}

	redirect := func(on bool) {
		t.Helper()
		id := g.mustPatch(map[string]any{"networks": map[string]any{iotID: map[string]any{"ntp": map[string]any{"redirect_hardcoded": on}}}})
		if r := g.apply(id); r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
	}

	redirect(true)
	r = b.until(t, "the redirect answers a query to the real server's address", a, server, func(r sntpReply) bool { return r.Replied && r.Stratum == 2 })
	// the strict client of sntp.py takes only a reply from the address and port it sent the query to
	if !r.StrictOK || r.From != testbed.ServerAddr || r.FromPort != 123 || r.RefIDIP != testbed.LAN0Gateway {
		t.Errorf("the redirected answer: %+v", r)
	}
	seen := queriesIn(real)
	for i := 0; i < 3; i++ {
		if r := b.ask(t, a, server); !r.StrictOK || r.Stratum != 2 {
			t.Errorf("a redirected query: %+v", r)
		}
	}
	if n := queriesIn(real); n != seen {
		t.Errorf("the real server saw %d queries that the gateway redirects", n-seen)
	}
	redirect(false)
	b.until(t, "the real server answers again once the network does not redirect", a, server, func(r sntpReply) bool { return r.Replied && r.RefIDASCII == "REAL" })
}

// M34 level 1: `offset +400d` on one device shifts only that device's answers by 400 days, in the receive and the transmit
// time stamps, the device next to it gets the correct time, the counters of the overlay and of the device show it, and
// removing the overlay ends it. chrony and ntpdig see the offset of the device.
func TestAnOffsetShiftsOnlyTheAnswersOfItsDeviceAndEndsWithTheOverlayOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t, withDevices)
	a, bb := b.top.A, b.top.B
	for _, ns := range []*testbed.Namespace{a, bb} {
		if r := b.best(t, ns, ntpGateway, 3); !r.correct(clockTolerance()) {
			t.Fatalf("before the fault a device gets the time %v off", r.shift())
		}
	}
	id := b.overlay(t, `{"target":{"device":"`+clientA+`"},"time":{"offset":"+400d"}}`)
	got := b.until(t, "A gets the time of the fault", a, ntpGateway, func(r sntpReply) bool { return r.near(fault400d, time.Minute) })
	if !got.StrictOK || got.Stratum != 2 {
		t.Errorf("a faulted answer is a normal one: %+v", got)
	}
	if r := b.ask(t, bb, ntpGateway); !r.correct(clockTolerance()) {
		t.Errorf("B, next to the faulted A, is %v off", r.shift())
	}
	// the receive time stamp is shifted, too: the offset of RFC 5905 is the fault's, as the client computes it from all four
	if r := b.best(t, a, ntpGateway, 3); r.Receive == nil || !within(secs(*r.Receive-r.T1), fault400d-time.Minute, fault400d+time.Minute) {
		t.Errorf("the receive time stamp of the faulted answer: %+v", r)
	}
	if res := b.ntpdig(t, a, testbed.LAN0Gateway); !res.Took || abs(res.Offset-fault400d) > time.Minute {
		t.Errorf("ntpdig on A: %+v", res)
	}
	if res := b.ntpdig(t, bb, testbed.LAN0Gateway); !res.Took || abs(res.Offset) > clockTolerance() {
		t.Errorf("ntpdig on B: %+v", res)
	}
	if res := b.chronyQ(t, a, testbed.LAN0Gateway); !res.Took || abs(abs(res.Offset)-fault400d) > time.Minute {
		t.Errorf("chrony on A measured %v (took %v):\n%s", res.Offset, res.Took, res.Output)
	}

	// the counters: the overlay, the device, and the log that names the device and the fault
	waitFor(t, 20*time.Second, "the overlay counts the queries it shaped", func() bool { return b.packets(t, id) >= 3 })
	b.logged(t, "the log names A, the fault and the offset", func(e ntpLogged) bool {
		return e.Device == espID && e.Fault == id && e.HasOffset && abs(time.Duration(e.OffsetMs)*time.Millisecond-fault400d) < time.Minute
	})
	waitFor(t, 20*time.Second, "the API counts the answers per fault for A and none for B", func() bool {
		_, answersA, okA := b.deviceState(t, espID)
		qB, answersB, okB := b.deviceState(t, ntpDevBID)
		return okA && okB && answersA[id] >= 3 && qB >= 1 && len(answersB) == 0
	})

	b.remove(t, id)
	b.until(t, "A gets the host's time again", a, ntpGateway, func(r sntpReply) bool { return r.correct(clockTolerance()) })
	if res := b.ntpdig(t, a, testbed.LAN0Gateway); !res.Took || abs(res.Offset) > clockTolerance() {
		t.Errorf("ntpdig on A after the fault: %+v", res)
	}
}

// M34 level 1: the `absolute` presets 2038 (the overflow of a signed 32-bit time_t) and NTP era 1 (the rollover of NTP's 32-bit
// seconds) are served within a second of the preset instant plus the time since the fault took effect, and the client, which
// shares no code with the server, reads them in the right era.
func TestTheAbsolutePresets2038AndNTPEra1AreServedWithinASecondOfTheirInstantPlusTheElapsedTimeOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t)
	a := b.top.A
	for _, p := range []struct{ name, instant string }{
		{"2038", "2038-01-19T03:09:07Z"},                    // 2^31-1 minus five minutes
		{"NTP era 1", "2036-02-07T06:23:16Z"},               // the rollover of the 32-bit seconds minus five minutes
		{"just after the rollover", "2036-02-07T06:28:30Z"}, // the seconds are small again, in era 1
		{"1970", "1970-01-01T00:00:00Z"},                    // the epoch of Unix time, before the NTP epoch plus 70 years
	} {
		want, err := time.Parse(time.RFC3339, p.instant)
		if err != nil {
			t.Fatal(err)
		}
		id := b.overlay(t, `{"target":{"network":"IoT"},"time":{"absolute":"`+p.instant+`"}}`)
		since := b.since(t, id)
		var r sntpReply
		waitFor(t, 40*time.Second, "A is served "+p.name, func() bool {
			r = b.ask(t, a, ntpGateway, "--timeout", "1.5")
			return r.Replied && r.Transmit != nil && abs(r.served().Sub(want)) < time.Hour
		})
		// the instant the fault took effect, then the clock running on: the instant plus the time since
		client := time.Unix(0, 0).Add(secs((r.T1 + r.T4) / 2))
		expected := want.Add(client.Sub(since))
		if d := r.served().Sub(expected); abs(d) > time.Second {
			t.Errorf("%s: served %v, want %v (the preset plus %v since it took effect), %v off", p.name, r.served().UTC(), expected.UTC(), client.Sub(since), d)
		}
		if !r.StrictOK {
			t.Errorf("%s: a correct client would not take the answer: %+v", p.name, r)
		}
		b.remove(t, id)
		b.until(t, "the host's time is back after "+p.name, a, ntpGateway, func(r sntpReply) bool { return r.correct(clockTolerance()) })
	}
}

// M34 level 1: `skew_ppm 1000` adds 10 ms over 10 s: the served time drifts from the moment the fault took effect.
func TestASkewOf1000PPMAddsTenMillisecondsOverTenSecondsOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t)
	a := b.top.A
	id := b.overlay(t, `{"target":{"network":"IoT"},"time":{"skew_ppm":1000}}`)
	// the drift is measured at its two ends: the answer with the shortest round trip of five, the offset of RFC 5905
	b.until(t, "the skew is served", a, ntpGateway, func(r sntpReply) bool { return r.Replied && r.Transmit != nil && b.packets(t, id) >= 1 })
	first := b.best(t, a, ntpGateway, 5)
	time.Sleep(10 * time.Second)
	second := b.best(t, a, ntpGateway, 5)
	drift := second.measured() - first.measured()
	// the drift follows the time between the two answers, which is a little more than 10 s
	elapsed := secs((second.T1+second.T4)/2 - (first.T1+first.T4)/2)
	want := time.Duration(float64(elapsed) * 1000 / 1e6)
	tol := 3 * time.Millisecond
	if !testbed.Accurate() {
		tol = 100 * time.Millisecond
	}
	if abs(drift-want) > tol {
		t.Errorf("the served time drifted %v in %v, want %v (1000 ppm) within %v", drift, elapsed, want, tol)
	}
	if want < 9*time.Millisecond || want > 12*time.Millisecond {
		t.Errorf("the test waited %v for a drift of %v: not the 10 s of the plan", elapsed, want)
	}
	// it is a drift on top of the correct time, not a jump: the first answer is within the drift of the first seconds
	if abs(first.measured()) > time.Second {
		t.Errorf("the first answer is %v off", first.measured())
	}

	// on top of an offset: the drift runs from the moment the fault took effect, here backwards
	b.remove(t, id)
	id = b.overlay(t, `{"target":{"network":"IoT"},"time":{"offset":"-1d","skew_ppm":-1000}}`)
	since := b.since(t, id)
	b.until(t, "the offset with a skew is served", a, ntpGateway, func(r sntpReply) bool { return r.near(-24*time.Hour, time.Minute) })
	time.Sleep(3 * time.Second)
	r := b.best(t, a, ntpGateway, 5)
	client := time.Unix(0, 0).Add(secs((r.T1 + r.T4) / 2))
	wantShift := -24*time.Hour - time.Duration(float64(client.Sub(since))*1000/1e6)
	if d := r.shift() - wantShift; abs(d) > tol {
		t.Errorf("-1d and -1000 ppm %v after the fault took effect: the served time is %v from the client's clock, want %v within %v", client.Sub(since), r.shift(), wantShift, tol)
	}
}

// M34 level 1: a leap fault sets leap indicator 1 (insert) or 2 (delete) in the answer, and with the preset the time a few
// minutes before midnight UTC at the end of a month.
func TestALeapFaultSetsTheLeapIndicatorAndServesTheMinutesBeforeMidnightOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t)
	a := b.top.A
	for _, c := range []struct {
		leap string
		li   int
	}{{"insert", 1}, {"delete", 2}} {
		id := b.overlay(t, `{"target":{"network":"IoT"},"time":{"leap":"`+c.leap+`","absolute":"2026-12-31T23:55:00Z"}}`)
		since := b.since(t, id)
		r := b.until(t, "A is served the leap "+c.leap, a, ntpGateway, func(r sntpReply) bool { return r.Replied && r.Leap == c.li })
		want := time.Date(2026, 12, 31, 23, 55, 0, 0, time.UTC).Add(time.Since(since))
		if d := r.served().Sub(want); abs(d) > 5*time.Second {
			t.Errorf("leap %s: served %v, want the minutes before midnight (%v)", c.leap, r.served().UTC(), want.UTC())
		}
		if r.Stratum != 2 || r.Mode != 4 {
			t.Errorf("leap %s: a leap indicator rides on an ordinary answer: %+v", c.leap, r)
		}
		b.remove(t, id)
		b.until(t, "no leap indicator after "+c.leap, a, ntpGateway, func(r sntpReply) bool { return r.Replied && r.Leap == 0 })
	}
	// a leap without a time keeps the host's time
	id := b.overlay(t, `{"target":{"network":"IoT"},"time":{"leap":"insert"}}`)
	if r := b.until(t, "the leap indicator alone", a, ntpGateway, func(r sntpReply) bool { return r.Replied && r.Leap == 1 }); !r.correct(clockTolerance()) {
		t.Errorf("a leap indicator without a time served %v off", r.shift())
	}
	b.remove(t, id)
}

// M34 level 1: `unsynchronized` (leap indicator 3, stratum 16) makes chrony reject the server, and a careless client takes
// the time: the real client is the control of the fault, the careless one of the test.
func TestAnUnsynchronizedAnswerMakesChronyRejectTheServerOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t)
	a := b.top.A
	if res := b.chronyQ(t, a, testbed.LAN0Gateway); !res.Took {
		t.Fatalf("chrony does not take the server before the fault, so its rejection below would prove nothing:\n%s", res.Output)
	}
	id := b.overlay(t, `{"target":{"network":"IoT"},"time":{"answer":"unsynchronized"}}`)
	r := b.until(t, "the unsynchronized answer", a, ntpGateway, func(r sntpReply) bool { return r.Replied && r.Leap == 3 })
	if r.Stratum != 16 || r.Mode != 4 || r.StrictOK || !r.CarelessOK {
		t.Errorf("an unsynchronized answer: %+v", r)
	}
	if res := b.chronyQ(t, a, testbed.LAN0Gateway); res.Took {
		t.Errorf("chrony took an unsynchronized server and measured %v:\n%s", res.Offset, res.Output)
	}
	waitFor(t, 20*time.Second, "the overlay counts the queries", func() bool { return b.packets(t, id) >= 2 })
	b.remove(t, id)
	b.until(t, "synchronized again", a, ntpGateway, func(r sntpReply) bool { return r.StrictOK && r.Stratum == 2 })
}

// M34 level 1: a kiss-of-death RATE lengthens the poll interval of chrony (the counters of the server show the queries per
// time), a DENY is not taken as time, and both are stratum 0 with the kiss code as reference id. RFC 5905 tells a client to stop
// using a server that says DENY or RSTR; chrony (4.8, ntp_core.c) does not: it reacts to RATE only and treats the other codes as
// an unsynchronized answer, so that it goes on asking at its normal interval. The test asserts what chrony does, and the
// stopping is for the client under test to show.
func TestAKissOfDeathRATELengthensThePollIntervalOfChronyAndDENYIsNotTakenOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t, withDevices)
	a := b.top.A
	window := 20 * time.Second
	// queries counts what the server received while f ran
	queries := func(f func()) int64 {
		before := b.srv.Counters().Queries
		f()
		return b.srv.Counters().Queries - before
	}
	pollingFor := func(d time.Duration) func() {
		return func() {
			p := b.startChronyd(t, a, testbed.LAN0Gateway)
			defer p.Stop()
			time.Sleep(d)
		}
	}

	// the control: chrony set to poll every second asks about once a second
	normal := queries(pollingFor(window))
	if normal < 8 {
		t.Fatalf("chrony asked %d times in %v without a fault, so a smaller number below would prove nothing", normal, window)
	}

	// RATE: the answer is stratum 0 with the code, and chrony asks less often than without it
	rate := b.overlay(t, `{"target":{"device":"`+clientA+`"},"time":{"answer":"kiss_of_death","kiss_code":"RATE"}}`)
	r := b.until(t, "the RATE answer", a, ntpGateway, func(r sntpReply) bool { return r.Replied && r.Stratum == 0 })
	if r.RefIDASCII != "RATE" || r.Leap != 3 || r.Mode != 4 || r.StrictOK {
		t.Errorf("a RATE answer: %+v", r)
	}
	limited := queries(pollingFor(window))
	if limited*2 > normal {
		t.Errorf("chrony asked %d times in %v under RATE and %d times without it: its poll interval did not get longer", limited, window, normal)
	}
	waitFor(t, 20*time.Second, "the API counts the RATE answers", func() bool {
		_, answers, ok := b.deviceState(t, espID)
		return ok && answers[rate] >= limited
	})
	b.remove(t, rate)

	// DENY: chrony does not take the server (it asks on: only RATE changes its behaviour)
	deny := b.overlay(t, `{"target":{"device":"`+clientA+`"},"time":{"answer":"kiss_of_death","kiss_code":"DENY"}}`)
	r = b.until(t, "the DENY answer", a, ntpGateway, func(r sntpReply) bool { return r.Replied && r.Stratum == 0 })
	if r.RefIDASCII != "DENY" || r.Leap != 3 {
		t.Errorf("a DENY answer: %+v", r)
	}
	if res := b.chronyQ(t, a, testbed.LAN0Gateway); res.Took {
		t.Errorf("chrony took a server that said DENY: %v\n%s", res.Offset, res.Output)
	}
	denied := queries(pollingFor(window))
	t.Logf("chrony asked %d times in %v under DENY and %d times without a fault", denied, window, normal)
	if denied == 0 {
		t.Errorf("the server received no query in %v under DENY", window)
	}
	b.remove(t, deny)

	// RSTR is a kiss code, too
	rstr := b.overlay(t, `{"target":{"device":"`+clientA+`"},"time":{"answer":"kiss_of_death","kiss_code":"RSTR"}}`)
	if r := b.until(t, "the RSTR answer", a, ntpGateway, func(r sntpReply) bool { return r.Replied && r.Stratum == 0 }); r.RefIDASCII != "RSTR" {
		t.Errorf("a RSTR answer: %+v", r)
	}
	b.remove(t, rstr)
}

// M34 level 1: the invalid answers. `origin_mismatch` is discarded by chrony and by a correct client (the origin check is its
// replay protection) and accepted by a deliberately careless one; `zero_transmit` and `wrong_mode` are discarded too. The log
// of the API names the answer, so the check of a run can flag a device that took it.
func TestAnOriginMismatchIsDiscardedByChronyAndAcceptedByACarelessClientOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t)
	a := b.top.A
	id := b.overlay(t, `{"target":{"network":"IoT"},"time":{"answer":"origin_mismatch"}}`)
	r := b.until(t, "the answer with the wrong origin", a, ntpGateway, func(r sntpReply) bool { return r.Replied && !r.OriginOK })
	if r.StrictOK || !r.CarelessOK || r.Mode != 4 || r.Stratum != 2 || r.Size != 48 {
		t.Errorf("an origin mismatch: %+v", r)
	}
	if res := b.chronyQ(t, a, testbed.LAN0Gateway); res.Took {
		t.Errorf("chrony took an answer whose origin is not its transmit time stamp: %v\n%s", res.Offset, res.Output)
	}
	b.logged(t, "the log names the answer", func(e ntpLogged) bool { return e.Fault == id && e.Answer == "origin_mismatch" })
	b.remove(t, id)

	id = b.overlay(t, `{"target":{"network":"IoT"},"time":{"answer":"zero_transmit"}}`)
	r = b.until(t, "the answer without a transmit time stamp", a, ntpGateway, func(r sntpReply) bool { return r.Replied && r.Transmit == nil })
	if r.StrictOK || !r.CarelessOK || !r.OriginOK {
		t.Errorf("a zero transmit time stamp: %+v", r)
	}
	b.logged(t, "the log names the zero transmit answer", func(e ntpLogged) bool { return e.Fault == id && e.Answer == "zero_transmit" })
	b.remove(t, id)

	id = b.overlay(t, `{"target":{"network":"IoT"},"time":{"answer":"wrong_mode"}}`)
	r = b.until(t, "the answer in the wrong mode", a, ntpGateway, func(r sntpReply) bool { return r.Replied && r.Mode != 4 })
	if r.StrictOK || !r.CarelessOK {
		t.Errorf("a wrong mode: %+v", r)
	}
	b.logged(t, "the log names the wrong mode answer", func(e ntpLogged) bool { return e.Fault == id && e.Answer == "wrong_mode" })
	b.remove(t, id)
	b.until(t, "a correct answer after the invalid ones", a, ntpGateway, func(r sntpReply) bool { return r.StrictOK })
}

// M34 level 1: `silence` leaves the query unanswered and counted, while DNS of the same device works (unlike a drop rule it acts
// only on NTP and counts), and the answer is back when the fault goes.
func TestSilenceLeavesTheQueryUnansweredAndCountedWhileDNSOfTheSameDeviceWorksOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t, withDNS, withDevices)
	a := b.top.A
	b.until(t, "A is answered", a, ntpGateway, func(r sntpReply) bool { return r.Replied })
	id := b.overlay(t, `{"target":{"device":"`+clientA+`"},"time":{"answer":"silence"}}`)
	waitFor(t, 30*time.Second, "A gets no answer", func() bool { return !b.ask(t, a, ntpGateway, "--timeout", "1.5").Replied })
	for i := 0; i < 2; i++ {
		if r := b.ask(t, a, ntpGateway, "--timeout", "1.5"); r.Replied {
			t.Errorf("an answer to a silenced device: %+v", r)
		}
	}
	// DNS of the same device works while its NTP is silent
	if out, err := digA(b.top, a, testbed.LAN0Gateway); err != nil || out != "203.0.113.77" {
		t.Errorf("DNS of the silenced device: %q %v", out, err)
	}
	// and a device next to it is answered
	if r := b.ask(t, b.top.B, ntpGateway); !r.correct(clockTolerance()) {
		t.Errorf("B next to the silenced A: %+v", r)
	}
	// the queries are counted: by the overlay, by the server and by the API, with the answer `silence` in the log
	waitFor(t, 20*time.Second, "the overlay counts the unanswered queries", func() bool { return b.packets(t, id) >= 3 })
	if c := b.srv.Counters(); c.Silenced < 3 {
		t.Errorf("the server counted %d silenced queries, want at least 3: %+v", c.Silenced, c)
	}
	b.logged(t, "the log has the silenced query of A", func(e ntpLogged) bool { return e.Device == espID && e.Fault == id && e.Answer == "silence" })
	waitFor(t, 20*time.Second, "the API counts the silenced queries of the device", func() bool {
		d, ok := b.g.srv.NTPDeviceState(espID)
		return ok && d.Silenced >= 3
	})

	b.remove(t, id)
	b.until(t, "A is answered again", a, ntpGateway, func(r sntpReply) bool { return r.correct(clockTolerance()) })
}

// M34 level 1 (D36): a destination selector on a hostname affects only the queries to the addresses that name resolved to for the
// device: the proxy fills the DNS-derived set when A resolves the name, the redirect follows, and a query to another server is
// answered by that server.
func TestADestinationSelectorOnAHostnameAffectsOnlyQueriesToTheAddressesOfThatNameOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t, withDNS)
	a := b.top.A
	b.startFakeNTP(t, b.top.Server, testbed.ServerAddr, "REAL")
	b.startFakeNTP(t, b.top.Server, testbed.ServerAddr2, "REL2")
	broker, other := testbed.ServerAddr+":123", testbed.ServerAddr2+":123"
	resolve := func(name string) string {
		out, err := a.Run(context.Background(), "dig", "+short", "+time=2", "+tries=1", "@"+testbed.LAN0Gateway, name)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(out)
	}

	id := b.overlay(t, `{"target":{"network":"IoT"},"time":{"offset":"+400d","destination":{"hostname":"`+brokerHost+`"}}}`)
	// A resolves both names; the proxy reports the address of the selected one before it answers
	var r sntpReply
	waitFor(t, 60*time.Second, "A is served the fault by the address that broker.test resolved to", func() bool {
		if resolve(brokerHost) != testbed.ServerAddr || resolve(otherHost) != testbed.ServerAddr2 {
			return false
		}
		r = b.ask(t, a, broker, "--timeout", "1.5")
		return r.Replied && r.Stratum == 2 && r.near(fault400d, time.Minute)
	})
	if !r.StrictOK || r.From != testbed.ServerAddr || r.FromPort != 123 {
		t.Errorf("the answer for the address of broker.test: %+v", r)
	}
	// the other name's address is not selected: its own server answers, with its own time
	o := b.ask(t, a, other)
	if !o.Replied || o.RefIDASCII != "REL2" || o.From != testbed.ServerAddr2 || !o.correct(clockTolerance()) {
		t.Errorf("the server of other.test: %+v", o)
	}
	// and the gateway address is not part of a selector that names a server
	if g := b.ask(t, a, ntpGateway); !g.correct(clockTolerance()) || g.Stratum != 2 {
		t.Errorf("the gateway address under a hostname selector: %+v", g)
	}
	// the server does not name the server for a hostname (it is a set of addresses); the port tells the selector
	b.logged(t, "the log names the fault and the port of the selector", func(e ntpLogged) bool {
		return e.Fault == id && e.Port >= 12301
	})
	b.remove(t, id)
	if r := b.until(t, "the real server answers again", a, broker, func(r sntpReply) bool { return r.Replied && r.RefIDASCII == "REAL" }); !r.correct(clockTolerance()) {
		t.Errorf("the real server after the fault: %+v", r)
	}
}

// M34 level 1 (D36): each distinct destination selector of an active time fault gets a port of its own on the server, the server
// tells the server asked from the port, and the scope without a selector arrives on 123. The ports are read from the server's
// configuration, not assumed; a port closes with its fault.
func TestEachDestinationSelectorGetsAPortOfItsOwnAndTheServerTellsTheServerAskedFromItOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t)
	a := b.top.A
	everywhere := b.overlay(t, `{"target":{"network":"IoT"},"time":{"offset":"-1d"}}`)
	first := b.overlay(t, `{"target":{"network":"IoT"},"time":{"absolute":"1970-01-01T00:00:00Z","destination":{"cidr":"`+testbed.ServerAddr+`/32"}}}`)
	second := b.overlay(t, `{"target":{"network":"IoT"},"time":{"offset":"+400d","destination":{"cidr":"`+testbed.ServerAddr2+`/32"}}}`)

	server1, server2, internet := testbed.ServerAddr+":123", testbed.ServerAddr2+":123", testbed.InternetAddr+":123"
	b.until(t, "the server for the first address", a, server1, func(r sntpReply) bool { return r.Replied && r.served().Year() == 1970 })
	b.until(t, "the server for the second address", a, server2, func(r sntpReply) bool { return r.near(fault400d, time.Minute) })
	// the fault without a selector covers every other server, the gateway's address included, newest selector first
	for _, to := range []string{ntpGateway, internet} {
		if r := b.until(t, "the fault for every server answers "+to, a, to, func(r sntpReply) bool { return r.near(-24*time.Hour, time.Minute) }); !r.StrictOK {
			t.Errorf("%s: %+v", to, r)
		}
	}

	// every answer came from the address asked (the strict client takes nothing else), and the log knows the server from the port
	e1 := b.logged(t, "the log has the first server", func(e ntpLogged) bool { return e.Fault == first && e.Server == server1 })
	e2 := b.logged(t, "the log has the second server", func(e ntpLogged) bool { return e.Fault == second && e.Server == server2 })
	if e1.Port < 12301 || e2.Port < 12301 || e1.Port == e2.Port {
		t.Errorf("the ports of the selectors are %d and %d, want two different ones from 12301", e1.Port, e2.Port)
	}
	b.logged(t, "the scope without a selector arrives on 123", func(e ntpLogged) bool { return e.Fault == everywhere && e.Port == 123 })

	// the configuration lists the ports, the server listens on exactly those, and a port closes with its fault (the ports of the
	// selectors that remain may move up: they are taken in the order of the selectors)
	listens := func() []int {
		ports := append([]int(nil), b.srv.Config().Listen...)
		sort.Ints(ports)
		return ports
	}
	sockets := func() []int {
		out, err := exec.Command("ip", "netns", "exec", b.ns, "ss", "-H", "-lun").CombinedOutput()
		if err != nil {
			t.Fatalf("ss in the service namespace: %v %s", err, out)
		}
		var ports []int
		for _, m := range regexp.MustCompile(`:(\d+)\s`).FindAllStringSubmatch(string(out), -1) {
			if p, _ := strconv.Atoi(m[1]); p == 123 || p >= 12301 {
				ports = append(ports, p)
			}
		}
		sort.Ints(ports)
		return ports
	}
	if l := listens(); len(l) != 3 || l[0] != 123 || fmt.Sprint(l) != fmt.Sprint([]int{123, min(e1.Port, e2.Port), max(e1.Port, e2.Port)}) {
		t.Errorf("the server listens on %v, want 123, %d and %d", l, e1.Port, e2.Port)
	}
	if got, want := sockets(), listens(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("the sockets of the service namespace are on %v, the configuration says %v", got, want)
	}
	b.remove(t, first)
	waitFor(t, 30*time.Second, "the port of the removed fault is closed", func() bool {
		l := listens()
		return len(l) == 2 && fmt.Sprint(sockets()) == fmt.Sprint(l)
	})
	// the server of the removed fault is asked like any other, the other one still has its fault, on whatever port it has now
	b.until(t, "the first server is covered by the fault for every server", a, server1, func(r sntpReply) bool { return r.near(-24*time.Hour, time.Minute) })
	if r := b.ask(t, a, server2); !r.near(fault400d, time.Minute) || !r.StrictOK {
		t.Errorf("the second server after the first fault went: %+v", r)
	}
}

// offsetOf is the offset of RFC 5905 that a client would apply, of the answer with the shortest round trip of n queries.
func (b *ntpBed) offsetOf(t *testing.T, from *testbed.Namespace, server string, n int) time.Duration {
	t.Helper()
	return b.best(t, from, server, n).measured()
}

// M34 level 1: a latency fault on udp/123, upload only, shifts the offset a client measures by about half of it (the client
// assumes a symmetric path: an asymmetry of d shifts its offset by d/2). It is a fault of the impairment family: the time family
// has no delay of its own. chrony measures the same.
func TestALatencyFaultOnUDP123UploadOnlyShiftsTheMeasuredOffsetByHalfOfItOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t)
	a := b.top.A
	b.until(t, "A is answered", a, ntpGateway, func(r sntpReply) bool { return r.Replied })
	base := b.offsetOf(t, a, ntpGateway, 5)
	chronyBase := b.chronyQ(t, a, testbed.LAN0Gateway)
	if !chronyBase.Took {
		t.Fatalf("chrony does not take the server:\n%s", chronyBase.Output)
	}

	const delay = 400 * time.Millisecond
	id := b.overlay(t, `{"target":{"network":"IoT"},"fault":{"upload":{"latency":"400ms"},"protocol":"udp","ports":[123]}}`)
	tol := 40 * time.Millisecond
	if !testbed.Accurate() {
		tol = 150 * time.Millisecond
	}
	waitFor(t, 40*time.Second, "the latency applies to the queries", func() bool { return b.best(t, a, ntpGateway, 3).rtt() > delay/2 })
	shifted := b.offsetOf(t, a, ntpGateway, 5)
	if d := shifted - base; abs(d-delay/2) > tol {
		t.Errorf("a latency of %v on the upload moved the offset by %v (%v to %v), want about %v within %v", delay, d, base, shifted, delay/2, tol)
	}
	res := b.chronyQ(t, a, testbed.LAN0Gateway)
	if !res.Took || abs(abs(res.Offset)-delay/2) > 2*tol {
		t.Errorf("chrony measured %v (took %v) under an upload latency of %v, want about %v:\n%s", res.Offset, res.Took, delay, delay/2, res.Output)
	}
	// other traffic is not slowed by a selector on NTP
	if ping := testbed.MustPing(t, a, testbed.ServerAddr, 3, 200*time.Millisecond); ping.Median() > 100*time.Millisecond {
		t.Errorf("a ping took %v: the fault on udp/123 slowed other traffic", ping.Median())
	}
	b.remove(t, id)
	b.until(t, "no shift after the fault", a, ntpGateway, func(r sntpReply) bool { return r.Replied && abs(r.measured()) < tol })
}

// wgClient brings up a client of the WireGuard hub lab-hub on the remote client of the topology and returns the gateway's tunnel
// address. The client is a test-role one, so the gateway's services are reachable at the tunnel address; the networks of
// `reachable` are put into its AllowedIPs, so that it can address a host of one of them through the tunnel.
func (b *ntpBed) wgClient(t *testing.T, reachable ...map[string]any) string {
	t.Helper()
	g, top := b.g, b.top
	client := map[string]any{"name": "rC", "address": "10.99.0.4", "keepalive": "1s", "key": map[string]any{"mode": "generated"}}
	if len(reachable) > 0 {
		list := make([]any, len(reachable))
		for i, r := range reachable {
			list[i] = r
		}
		client["reachable"] = list
	}
	id := g.mustPatch(map[string]any{"networks": map[string]any{hubID: map[string]any{"clients": map[string]any{
		"6e7f8091-aabb-4c2d-8e3f-4a5b6c7d8e9f": client,
	}}}})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	r := g.do("GET", "/networks/lab-hub/clients/rC/export", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	conf := t.TempDir() + "/wgrc.conf"
	if err := os.WriteFile(conf, r.Body, 0o600); err != nil {
		t.Fatal(err)
	}
	top.RC.Must("wg-quick", "up", conf)
	waitFor(t, 30*time.Second, "the tunnel carries traffic", func() bool { return pingOK(top.RC, "", "10.99.0.1") })
	return "10.99.0.1"
}

// M34 level 1: a WireGuard client (no DHCP) reaches the server at the gateway's tunnel address, is served the fault of its
// network, and a latency fault on udp/123 shifts its offset by half, like a device of a local network.
func TestAWireGuardClientIsServedLikeADeviceAndAnUploadLatencyShiftsItsOffsetByHalfOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t)
	gw := b.wgClient(t) + ":123"
	rc := b.top.RC
	r := b.until(t, "the WireGuard client is answered at the tunnel address", rc, gw, func(r sntpReply) bool { return r.Replied })
	if !r.StrictOK || r.Stratum != 2 || r.RefIDIP != "10.99.0.1" || r.From != "10.99.0.1" || !r.correct(clockTolerance()) {
		t.Errorf("the answer through the tunnel: %+v", r)
	}
	base := b.offsetOf(t, rc, gw, 5)

	id := b.overlay(t, `{"target":{"network":"lab-hub"},"time":{"offset":"+400d"}}`)
	b.until(t, "the client is served the fault of its network", rc, gw, func(r sntpReply) bool { return r.near(fault400d, time.Minute) })
	// a device of another network is not affected by the fault of this one
	if r := b.ask(t, b.top.A, ntpGateway); !r.correct(clockTolerance()) {
		t.Errorf("A in another network: %+v", r)
	}
	b.remove(t, id)
	b.until(t, "the correct time again", rc, gw, func(r sntpReply) bool { return r.correct(clockTolerance()) })

	const delay = 400 * time.Millisecond
	b.overlay(t, `{"target":{"network":"lab-hub"},"fault":{"upload":{"latency":"400ms"},"protocol":"udp","ports":[123]}}`)
	tol := 40 * time.Millisecond
	if !testbed.Accurate() {
		tol = 150 * time.Millisecond
	}
	waitFor(t, 40*time.Second, "the latency applies through the tunnel", func() bool { return b.best(t, rc, gw, 3).rtt() > delay/2 })
	if d := b.offsetOf(t, rc, gw, 5) - base; abs(d-delay/2) > tol {
		t.Errorf("a latency of %v on the upload moved the offset of the tunnel client by %v, want about %v within %v", delay, d, delay/2, tol)
	}
}

// M34 level 1 (Q3): a WireGuard client has no redirect_hardcoded, so a time fault that names the server a tunnel client asks is
// the only way it reaches the fault: the query to that server arrives on the tunnel interface, is redirected to the port of the
// selector, and the answer carries the fault and comes from the address and port asked. The server asked is a stand-in in the
// network of device A, which labels its answers and logs its queries and must never see the query.
func TestAWireGuardClientAskingAHardcodedServerIsServedTheFaultFromTheAskedServersAddressOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t)
	real := b.startFakeNTP(t, b.top.A, testbed.ClientAAddr, "REAL")
	b.wgClient(t, map[string]any{"network": iotID})
	rc := b.top.RC
	server := testbed.ClientAAddr + ":123"

	id := b.overlay(t, `{"target":{"network":"lab-hub"},"time":{"offset":"+400d","destination":{"cidr":"`+testbed.ClientAAddr+`/32"}}}`)
	r := b.until(t, "the tunnel client is served the fault by the address of the server it asked", rc, server, func(r sntpReply) bool {
		return r.Replied && r.Stratum == 2 && r.near(fault400d, time.Minute)
	})
	// the strict client takes only a reply from the address and port it sent the query to, and the reference id is the
	// gateway's tunnel address, not the stand-in's label
	if !r.StrictOK || r.From != testbed.ClientAAddr || r.FromPort != 123 || r.RefIDIP != "10.99.0.1" {
		t.Errorf("the answer for the hardcoded server through the tunnel: %+v", r)
	}
	if n := queriesIn(real); n != 0 {
		t.Errorf("the server asked saw %d queries that the gateway redirects", n)
	}
	e := b.logged(t, "the log has the query of the tunnel client with the server it asked", func(e ntpLogged) bool {
		return e.Fault == id && e.Server == server
	})
	if e.Port < 12301 {
		t.Errorf("the query arrived on port %d, want the port of the selector", e.Port)
	}
}

// M34 level 1: the server is not an amplifier. Control (6) and private (7) mode datagrams, short ones and other modes get no
// answer, however they are sized; a reply is 48 bytes and never larger than its query; and queries from the uplink and from the
// management network, from which nothing is redirected to the server, never reach it and get no answer.
func TestModes6And7AndQueriesFromTheUplinkAndTheManagementNetworkGetNoAnswerAndNoReplyIsLargerThanItsQueryOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t)
	a := b.top.A
	b.until(t, "A is answered", a, ntpGateway, func(r sntpReply) bool { return r.Replied })

	zeros := func(n int) string { return strings.Repeat("00", n) }
	for _, c := range []struct{ name, raw string }{
		{"control mode READVAR (12 bytes)", "160200010000000000000000"},
		{"control mode padded to 48 bytes", "1602" + zeros(46)},
		{"private mode MON_GETLIST_1 (8 bytes)", "1700032a00000000"},
		{"private mode padded to 48 bytes", "1700032a" + zeros(44)},
		{"private mode, a request of 468 bytes", "1700032a" + zeros(464)},
		{"version 4 mode 5 (broadcast)", "25" + zeros(47)},
		{"a client query of 20 bytes", "23" + zeros(19)},
		{"version 0", "03" + zeros(47)},
	} {
		before := b.srv.Counters().Ignored
		r := b.ask(t, a, ntpGateway, "--raw", c.raw, "--timeout", "1.5")
		if r.Replied {
			t.Errorf("%s was answered with %d bytes", c.name, r.Size)
		}
		waitFor(t, 10*time.Second, "the server counts "+c.name+" as ignored", func() bool { return b.srv.Counters().Ignored > before })
	}

	// the size of a reply: 48 bytes to 48, and 48 bytes to a request with extension fields of 1,000 bytes
	for _, pad := range []string{"0", "52", "1000"} {
		r := b.ask(t, a, ntpGateway, "--pad", pad)
		if !r.Replied || r.Size != 48 || r.Size > r.Sent {
			t.Errorf("a query of %d bytes was answered with %d bytes", r.Sent, r.Size)
		}
	}

	// the uplink and the management network: nothing is redirected, nothing listens on the gateway's addresses
	queries := b.srv.Counters().Queries
	for _, c := range []struct {
		name string
		from *testbed.Namespace
		to   string
	}{
		{"the server behind the uplink asking the uplink address", b.top.Server, testbed.UplinkGateway + ":123"},
		{"the management network asking the management address", b.top.Mgmt, testbed.MgmtGateway + ":123"},
		{"the server behind the uplink asking the gateway address of the test network", b.top.Server, ntpGateway},
		{"the remote client asking the uplink address", b.top.RC, testbed.UplinkGateway + ":123"},
		{"the management network asking the service namespace's address", b.top.Mgmt, "169.254.100.2:123"},
		{"the server behind the uplink asking the service namespace's address", b.top.Server, "169.254.100.2:123"},
	} {
		if r := b.ask(t, c.from, c.to, "--timeout", "1.5"); r.Replied {
			t.Errorf("%s was answered: %+v", c.name, r)
		}
	}
	if !pingOK(b.top.Server, "", testbed.UplinkGateway) {
		t.Errorf("the server behind the uplink does not reach the gateway at all, so its silence above proves nothing")
	}
	if n := b.srv.Counters().Queries; n != queries {
		t.Errorf("the server counted %d queries that came from the uplink or the management network", n-queries)
	}
	for _, e := range b.g.srv.NTPLog() {
		switch e.Client {
		case testbed.ServerAddr, testbed.MgmtPeer, testbed.RemoteClientAddr:
			t.Errorf("a query from %s is in the log: %+v", e.Client, e)
		}
	}
}

// M34 level 1: with the service namespace gone, NTP that is redirected fails closed: the query is dropped by the guard of the
// gateway and the real server behind the uplink is never asked, and the next apply brings the namespace and the answers back.
func TestRedirectedNTPFailsClosedWithoutTheServiceNamespaceOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t)
	g, top, a := b.g, b.top, b.top.A
	real := b.startFakeNTP(t, top.Server, testbed.ServerAddr, "REAL")
	server := testbed.ServerAddr + ":123"
	id := g.mustPatch(map[string]any{"networks": map[string]any{iotID: map[string]any{"ntp": map[string]any{"redirect_hardcoded": true}}}})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	b.until(t, "the redirect answers a query to the real server's address", a, server, func(r sntpReply) bool { return r.Replied && r.Stratum == 2 })
	seen := queriesIn(real)

	// the holder of the namespace goes, with the server in it
	b.stopServer()
	if out, err := exec.Command("ip", "netns", "delete", b.ns).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	waitFor(t, 30*time.Second, "svc0 goes with the namespace", func() bool {
		_, err := top.GW.Run(context.Background(), "ip", "link", "show", "dev", "svc0")
		return err != nil
	})
	before := counterPackets(top, "forward_drop")
	for _, to := range []string{server, ntpGateway} {
		if r := b.ask(t, a, to, "--timeout", "1.5"); r.Replied {
			t.Errorf("an answer to %s without a service namespace: %+v", to, r)
		}
	}
	if after := counterPackets(top, "forward_drop"); after <= before {
		t.Errorf("the redirected queries were not dropped by the guard: %d, then %d\n%s", before, after, top.GW.Must("nft", "list", "chain", "inet", "chaosgw", "forward"))
	}
	if n := queriesIn(real); n != seen {
		t.Errorf("the real server was asked %d times while the redirect had no namespace: the query leaked", n-seen)
	}

	// the next apply creates the namespace again, and a server in it answers again
	if err := g.e.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 60*time.Second, "svc0 is back", func() bool {
		_, err := top.GW.Run(context.Background(), "ip", "-br", "addr", "show", "dev", "svc0")
		return err == nil
	})
	b.startServer(t)
	b.until(t, "the redirected query is answered again", a, server, func(r sntpReply) bool { return r.Replied && r.Stratum == 2 })
	if n := queriesIn(real); n != seen {
		t.Errorf("the real server was asked %d times", n-seen)
	}
}

// M34 level 1: the host clock reported unsynchronized makes the server answer with stratum 16 and leap indicator 3, so that
// a correct client (chrony) ignores it; /system says it; the answer is normal again when the clock is.
func TestTheHostClockReportedUnsynchronizedMakesTheServerAnswerWithStratum16OnTheRealKernel(t *testing.T) {
	b := newNTPBed(t)
	a := b.top.A
	healthDetail := func() string {
		b.g.token = b.admin
		for _, c := range b.g.do("GET", "/system/health", nil, nil, nil).json(t)["components"].([]any) {
			if m := c.(map[string]any); m["name"] == "ntp" {
				d, _ := m["detail"].(string)
				return d
			}
		}
		return ""
	}
	if r := b.until(t, "the synchronized answer", a, ntpGateway, func(r sntpReply) bool { return r.Replied }); r.Stratum != 2 || r.Leap != 0 {
		t.Fatalf("a synchronized host: %+v", r)
	}
	if d := healthDetail(); strings.Contains(d, "not synchronized") {
		t.Errorf("the health report of a synchronized host: %q", d)
	}

	b.sync.set(ntpserver.Sync{})
	r := b.until(t, "the unsynchronized answer", a, ntpGateway, func(r sntpReply) bool { return r.Replied && r.Stratum == 16 })
	if r.Leap != 3 || r.Mode != 4 || r.StrictOK {
		t.Errorf("the answer of an unsynchronized host: %+v", r)
	}
	if res := b.chronyQ(t, a, testbed.LAN0Gateway); res.Took {
		t.Errorf("chrony took a server whose host clock is not synchronized: %v\n%s", res.Offset, res.Output)
	}
	if d := healthDetail(); !strings.Contains(d, "host clock is not synchronized") || !strings.Contains(d, "stratum 16") {
		t.Errorf("/system does not report the unsynchronized host clock: %q", d)
	}

	b.sync.set(ntpserver.Sync(ntpserver.Synchronized))
	b.until(t, "synchronized again", a, ntpGateway, func(r sntpReply) bool { return r.StrictOK && r.Stratum == 2 })
}

// M34 level 1: a connection to the NTS key-establishment port of the gateway (a device that uses NTS) is refused by the server,
// which cannot be the identity of the server the device meant, and reported once as an NTS-KE connection of the device.
func TestAnNTSKeyEstablishmentConnectionIsRefusedAndReportedForTheDeviceOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t, withDevices, withNTSKE)
	a := b.top.A
	b.until(t, "A is answered", a, ntpGateway, func(r sntpReply) bool { return r.Replied })
	// the connection is accepted at the TCP level (the redirect works) and closed without a key exchange
	for i := 0; i < 2; i++ {
		waitFor(t, 30*time.Second, "the NTS-KE port accepts a connection", func() bool {
			_, err := a.Run(context.Background(), "nc", "-z", "-w", "3", testbed.LAN0Gateway, "4460")
			return err == nil
		})
	}
	e := b.logged(t, "the log has the NTS-KE connection of A", func(e ntpLogged) bool { return e.Kind == "nts_ke" && e.Client == testbed.ClientAAddr })
	if e.Device != espID || e.Port != ntpserver.NTSKEPort {
		t.Errorf("the NTS-KE entry: %+v", e)
	}
	waitFor(t, 20*time.Second, "the API counts the NTS-KE connection of the device", func() bool {
		d, ok := b.g.srv.NTPDeviceState(espID)
		return ok && d.NTSKE >= 1
	})
	// once per device: the second connection is not another entry in a minute
	n := 0
	for _, l := range b.g.srv.NTPLog() {
		if l.Kind != nil && string(*l.Kind) == "nts_ke" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d NTS-KE entries for two connections of one device, want 1", n)
	}
}

// M34 level 1: a scenario with a time step runs on the real clock and the real kernel: the step shifts the served time of its target
// while the run lasts, the run's end takes the overlay out, and the time is the host's again. (The wait NTP query seen and the
// check NTP queried have the test below.)
func TestAScenarioWithATimeStepShiftsTheServedTimeUntilTheRunEndsOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t)
	g, a := b.g, b.top.A
	g.mintToken("overlays")
	r := g.postRun(inlineRun("time-run", iotTarget,
		step("shift", "0s", "time", map[string]any{"offset": "+400d"}),
		step("end", "25s", "restore", true),
	), "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	id := r.json(t)["id"].(string)
	g.waitStep(id, "shift", "applied")
	got := b.until(t, "A is served the time of the step", a, ntpGateway, func(r sntpReply) bool { return r.near(fault400d, time.Minute) })
	if !got.StrictOK {
		t.Errorf("%+v", got)
	}
	if n := len(g.overlaysOf(id)); n != 1 {
		t.Errorf("%d overlays while the step is on, want 1", n)
	}
	g.waitRun(id, "passed")
	if n := len(g.overlaysOf(id)); n != 0 {
		t.Errorf("%d overlays are left after the run", n)
	}
	b.until(t, "the host's time after the run", a, ntpGateway, func(r sntpReply) bool { return r.correct(clockTolerance()) })
}

// M34 level 1: a scenario with a time step, the wait NTP query seen and the check NTP queried, on the real clock and the real
// kernel. A client of the test network asks the gateway's server while the run waits; the query goes through the redirect to the
// server, which answers with the fault of the step and reports it to the API, and the wait ends at it. The check passes on the same
// query with the answer in its evidence, and the fault, which shaped the answer, is no zero hit.
func TestAScenarioWithATimeStepAWaitForTheQueryAndTheCheckSeesTheQueryOfARealClientOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t, withDevices)
	g, a := b.g, b.top.A
	g.mintToken("overlays")
	body := inlineRun("time-wait", iotTarget,
		step("shift", "0s", "time", map[string]any{"offset": "+400d"}),
		step("polled", "1s", "wait", map[string]any{"for": "ntp_query_seen", "timeout": "90s"}),
		step("end", "3s", "restore", true),
	)
	body["inline"].(map[string]any)["checks"] = []any{
		map[string]any{"ntp_queried": map[string]any{}, "window": map[string]any{"from": "shift", "until": "end"}},
	}
	r := g.postRun(body, "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	id := r.json(t)["id"].(string)
	overlay := g.waitStep(id, "shift", "applied")["overlay"].(string)

	// the client polls until the wait has ended; its first query after the step is the one the wait takes
	var last sntpReply
	waitFor(t, 90*time.Second, "the wait for the NTP query ends", func() bool {
		last = b.ask(t, a, ntpGateway, "--timeout", "1.5")
		return stepOf(g.run(id), "polled")["status"] == "applied"
	})
	if !last.Replied {
		t.Logf("the last query was not answered: %+v", last)
	}
	done := g.waitRun(id, "passed")

	c := done["checks"].([]any)[0].(map[string]any)
	if c["status"] != "passed" || c["type"] != "ntp_queried" || !strings.Contains(c["message"].(string), "a device asked the NTP server for the time") {
		t.Fatalf("the check: %v", c)
	}
	ev, _ := c["evidence"].([]any)
	if len(ev) != 1 {
		t.Fatalf("the evidence: %v", c["evidence"])
	}
	e := ev[0].(map[string]any)
	off, _ := e["offset_ms"].(float64)
	if e["kind"] != "ntp_query" || e["device"] != espID || e["fault"] != overlay || e["answer"] != "normal" || math.Abs(off-float64(fault400d.Milliseconds())) > 60000 {
		t.Errorf("the query in the evidence is not the one the fault of the step answered: %v", e)
	}
	if ws := warningCodes(done); len(ws) != 0 {
		t.Errorf("warnings %v: the fault shaped the queries of the client", done["warnings"])
	}
	if n := len(g.overlaysOf(id)); n != 0 {
		t.Errorf("%d overlays are left after the run", n)
	}
}

// ntpLoad is what one run of load against the server came to.
type ntpLoad struct {
	sent, answered, bad int64
	added               []time.Duration // the time the server added to each answer: transmit minus receive time stamp
	elapsed             time.Duration
}

// ntpLoadFrom sends queries to the gateway from the namespace of a device through the redirect, from workers sockets for length.
// With every zero each socket sends the next query as soon as the answer is in (a flood, to find what the server can answer);
// otherwise each socket sends one query per every (a steady load, to measure what the server adds when it is not saturated).
func ntpLoadFrom(ns string, workers int, length, every time.Duration) ntpLoad {
	var sent, answered, bad atomic.Int64
	var mu sync.Mutex
	var added []time.Duration
	start := time.Now()
	deadline := start.Add(length)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			conn, err := testbed.DialerIn(ns)(context.Background(), "udp", ntpGateway)
			if err != nil {
				bad.Add(1)
				return
			}
			defer func() { _ = conn.Close() }()
			var mine []time.Duration
			buf := make([]byte, 512)
			next := start.Add(every * time.Duration(w) / time.Duration(workers)) // the sockets do not all send at the same instant
			for i := uint64(1); time.Now().Before(deadline); i++ {
				if every > 0 {
					time.Sleep(time.Until(next))
					next = next.Add(every)
				}
				q := ntpserver.Packet{Version: 4, Mode: ntpserver.ModeClient, Transmit: ntpserver.Timestamp(i<<20 | 1)}
				if _, err := conn.Write(q.Marshal()); err != nil {
					bad.Add(1)
					return
				}
				sent.Add(1)
				_ = conn.SetReadDeadline(time.Now().Add(time.Second))
				n, err := conn.Read(buf)
				if err != nil {
					continue // a lost query or answer: counted by the difference
				}
				p, err := ntpserver.Parse(buf[:n])
				if err != nil || p.Origin != q.Transmit || p.Mode != ntpserver.ModeServer {
					bad.Add(1)
					continue
				}
				answered.Add(1)
				now := time.Now()
				mine = append(mine, p.Transmit.Time(now).Sub(p.Receive.Time(now)))
			}
			mu.Lock()
			added = append(added, mine...)
			mu.Unlock()
		}(w)
	}
	wg.Wait()
	return ntpLoad{sent: sent.Load(), answered: answered.Load(), bad: bad.Load(), added: added, elapsed: time.Since(start)}
}

func (l ntpLoad) rate() float64 { return float64(l.answered) / l.elapsed.Seconds() }
func (l ntpLoad) lost() float64 { return 1 - float64(l.answered)/float64(l.sent) }

// M34 acceptance (plan §3.10): the server answers 5,000 queries per second and adds less than a millisecond to an answer. The
// load comes from the namespace of a device, through the redirect (DNAT, the link into the service namespace and back through
// connection tracking), from many sockets at once, with the rate limit off. The time the server added is in the packet: the
// transmit time stamp minus the receive time stamp. Two runs measure two things: a flood shows what the server can answer, and
// a steady load of 5,000 queries per second shows what it adds to an answer when it is not saturated (under a flood the
// datagrams queue in the socket, and that wait is the load's, not the server's). Where the timing is meaningful (native or KVM)
// the figures are asserted and recorded in the measurement record; under software emulation they are only recorded and the test
// asserts that the load is answered at all.
func TestTheServerAnswersFiveThousandQueriesPerSecondThroughTheRedirectAndAddsLessThanAMillisecondOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t)
	a := b.top.A
	b.until(t, "A is answered", a, ntpGateway, func(r sntpReply) bool { return r.Replied })

	const (
		workers = 16
		length  = 5 * time.Second
		target  = 5000 // queries per second
	)
	flood := ntpLoadFrom(a.Name, workers, length, 0)
	steady := ntpLoadFrom(a.Name, workers, length, workers*time.Second/target)
	if len(flood.added) == 0 || len(steady.added) == 0 {
		t.Fatalf("no query was answered: %d of %d in the flood, %d of %d at the steady load", flood.answered, flood.sent, steady.answered, steady.sent)
	}
	p99 := testbed.Percentile(steady.added, 99)
	t.Logf("flood: %d of %d queries answered in %v by %d sockets: %.0f per second (the server added %v at the 99th percentile, median %v)", flood.answered, flood.sent, flood.elapsed, workers, flood.rate(), testbed.Percentile(flood.added, 99), testbed.Median(flood.added))
	t.Logf("steady: %d of %d queries answered in %v: %.0f per second; the server added %v at the 99th percentile (median %v)", steady.answered, steady.sent, steady.elapsed, steady.rate(), p99, testbed.Median(steady.added))
	if flood.bad+steady.bad > 0 {
		t.Errorf("%d answers were wrong or a socket failed", flood.bad+steady.bad)
	}
	low, high := float64(target), 1.0 // queries per second, milliseconds
	lost := flood.lost()
	rec := testbed.Measure(t, "NTP through the redirect")
	ms := float64(p99) / float64(time.Millisecond)
	if testbed.Accurate() {
		err := error(nil)
		if flood.rate() < low || lost > 0.01 {
			err = fmt.Errorf("%.0f queries per second answered with a loss of %.2f %%, want at least 5,000 per second with a loss below 1 %%", flood.rate(), lost*100)
		}
		_ = rec.Record("rate", testbed.Measurement{Quantity: "answers per second", Unit: "1/s", N: int(flood.sent), Configured: target, Value: flood.rate(), Low: &low, Loss: &lost}, err)
		if err != nil {
			t.Error(err)
		}
		err = nil
		if steadyLost := steady.lost(); steadyLost > 0.01 {
			err = fmt.Errorf("%.2f %% of the queries at the steady load of 5,000 per second were not answered, want less than 1 %%", steadyLost*100)
		} else if p99 >= time.Millisecond {
			err = fmt.Errorf("the server added %v at the 99th percentile at the steady load of 5,000 per second, want less than 1 ms", p99)
		}
		_ = rec.Record("added time", testbed.Measurement{Quantity: "added time at the 99th percentile at 5,000 queries per second", Unit: "ms", N: len(steady.added), Configured: 1, Value: ms, High: &high}, err)
		if err != nil {
			t.Error(err)
		}
		return
	}
	// under emulation the figures are recorded for the trend and the test asserts the function
	_ = rec.Record("rate", testbed.Measurement{Quantity: "answers per second", Unit: "1/s", N: int(flood.sent), Configured: target, Value: flood.rate(), Loss: &lost}, nil)
	_ = rec.Record("added time", testbed.Measurement{Quantity: "added time at the 99th percentile at 5,000 queries per second", Unit: "ms", N: len(steady.added), Configured: 1, Value: ms}, nil)
	if flood.answered < 500 || math.IsNaN(flood.rate()) {
		t.Errorf("only %d queries were answered under load", flood.answered)
	}
}

// lastReplies reads the replies a running sntp.py has printed so far.
func lastReplies(t *testing.T, p *testbed.Process) []sntpReply {
	t.Helper()
	var out []sntpReply
	for _, l := range strings.Split(p.Output(), "\n") {
		if !strings.HasPrefix(l, "{") || !strings.HasSuffix(l, "}") {
			continue
		}
		var r sntpReply
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatalf("sntp.py printed %q: %v", l, err)
		}
		out = append(out, r)
	}
	return out
}

// M34 level 1: a device that keeps its socket (one source port, a poll every few seconds, like ntpd and the SNTP clients of
// many firmwares) keeps its entry in the connection tracking, and a NAT chain sees only the first packet of an entry. The
// redirect of a time fault must reach it all the same, and its removal must release it: the engine deletes the tracked NTP flows
// when the redirects change. Without that the device would go on to the real server for up to two minutes after the fault
// arrived, and to the gateway's server after it ended, which is the silent miss that decision Q3 is against.
func TestADeviceThatKeepsItsSocketIsReachedByATimeFaultAndReleasedWhenItEndsOnTheRealKernel(t *testing.T) {
	b := newNTPBed(t)
	a := b.top.A
	b.startFakeNTP(t, b.top.Server, testbed.ServerAddr, "REAL")
	server := testbed.ServerAddr + ":123"
	p := a.Start("python3", "-I", filepath.Join(b.scripts, "sntp.py"), server, "--keep", "1", "--count", "600", "--interval", "0.3", "--timeout", "0.25")
	defer p.Stop()
	// the answer of the last query: the polls go on in the background, and each wait looks at what came after the change
	latest := func() sntpReply {
		if rs := lastReplies(t, p); len(rs) > 0 {
			return rs[len(rs)-1]
		}
		return sntpReply{}
	}
	waitFor(t, 30*time.Second, "the device polls the real server", func() bool {
		r := latest()
		return r.Replied && r.RefIDASCII == "REAL"
	})

	id := b.overlay(t, `{"target":{"network":"IoT"},"time":{"offset":"+400d"}}`)
	waitFor(t, 30*time.Second, "the device that keeps its socket gets the answer of the gateway's server for the fault", func() bool {
		r := latest()
		return r.near(fault400d, time.Minute) && r.Stratum == 2
	})

	b.remove(t, id)
	waitFor(t, 30*time.Second, "the device that keeps its socket is answered by the real server again once the fault is gone", func() bool {
		r := latest()
		return r.Replied && r.RefIDASCII == "REAL"
	})

	// every query came from one source port: the test saw the flows that were kept, not a series of new ones
	rs := lastReplies(t, p)
	for _, r := range rs {
		if r.SPort != rs[0].SPort {
			t.Fatalf("the client changed its source port: %d and %d", rs[0].SPort, r.SPort)
		}
	}
}
