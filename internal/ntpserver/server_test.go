package ntpserver

import (
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

func TestAServerWithoutAConfigurationAnswersOnlyItsHealthProbe(t *testing.T) {
	s, _ := newServer(t, nil)
	if _, e, ok := ask(t, s, dev1, Port); ok || e != nil {
		t.Fatalf("a query before the first configuration was answered or logged: %v", e)
	}
	if got := s.Counters().Unserved; got != 1 {
		t.Errorf("%d unserved queries, want 1", got)
	}
	reply, e := s.Process(Packet{Version: 4, Mode: ModeClient, Transmit: probeTransmit}.Marshal(), netip.MustParseAddr("127.0.0.1"), Port, time.Time{})
	if e != nil {
		t.Errorf("the probe is in the query log: %+v", e)
	}
	p, err := Parse(reply)
	if err != nil || p.Mode != ModeServer || p.Origin != probeTransmit {
		t.Fatalf("the probe was not answered: %+v %v", p, err)
	}
}

func TestANormalAnswerIsTheHostTimeAsAStratum2Server(t *testing.T) {
	s, clk := newServer(t, func(o *Options) {
		o.Sync = StaticSync{Synchronized: true, EstError: 2 * time.Millisecond, MaxError: 20 * time.Millisecond}
	})
	s.SetConfig(config(t, []int{Port}))
	clk.Advance(3 * time.Second)
	p, e, ok := ask(t, s, dev1, Port)
	if !ok {
		t.Fatal("no answer")
	}
	if p.Leap != LeapNone || p.Mode != ModeServer || p.Stratum != 2 || p.Version != 4 || p.Poll != 6 {
		t.Errorf("header: %+v", p)
	}
	if p.RefID != [4]byte{10, 20, 0, 1} {
		t.Errorf("the reference id is %v, want the gateway of the device's network", p.RefID)
	}
	if p.Origin != 0x1234_5678_9abc_def0 {
		t.Errorf("origin %#x is not the client's transmit timestamp", uint64(p.Origin))
	}
	now := clk.Now()
	near(t, "receive", p.Receive.Time(now), now, time.Microsecond)
	near(t, "transmit", served(p, now), now, time.Microsecond)
	near(t, "reference", p.Reference.Time(now), now, time.Second)
	if p.Reference.Time(now).After(now) {
		t.Error("the reference time is after the answer")
	}
	if p.RootDelay != shortFormat(2*time.Millisecond) || p.RootDispersion != shortFormat(20*time.Millisecond) {
		t.Errorf("root delay %#x, dispersion %#x are not the kernel's errors", p.RootDelay, p.RootDispersion)
	}
	if e == nil || e.Answer == nil || *e.Answer != model.NtpQueryLogEntryAnswerNormal || e.Fault != nil || *e.OffsetMs != 0 || e.Port != Port ||
		e.Client != "10.20.0.11" || *e.Version != 4 || e.ServedTime == nil || !e.ServedTime.Equal(now) || !e.Time.Equal(now) {
		t.Errorf("log entry: %+v", e)
	}
}

func TestTheVersionOfTheAnswerIsTheQueriesUpToVersion4(t *testing.T) {
	s, _ := newServer(t, nil)
	s.SetConfig(config(t, []int{Port}))
	for _, tc := range []struct{ query, want uint8 }{{1, 1}, {2, 2}, {3, 3}, {4, 4}, {5, 4}, {7, 4}} {
		reply, _ := s.Process(request(tc.query, 99), dev1, Port, time.Time{})
		p, err := Parse(reply)
		if err != nil || p.Version != tc.want {
			t.Errorf("version %d is answered with %d (%v)", tc.query, p.Version, err)
		}
	}
}

// Modes 6 and 7 are the control and private modes of ntpq and ntpdc; `monlist` is the amplification attack of the second.
// Nothing but a client query (mode 3) gets an answer, and a reply is 48 bytes, never larger than its request.
func TestOnlyAClientQueryIsAnsweredAndNoReplyIsLargerThanItsRequest(t *testing.T) {
	s, _ := newServer(t, nil)
	s.SetConfig(config(t, []int{Port}))
	for mode := uint8(0); mode < 8; mode++ {
		for _, version := range []uint8{0, 2, 3, 4} {
			req := Packet{Version: version, Mode: mode}.Marshal()
			if mode == ModePrivate {
				req = append(req[:0:0], 0x17, 0x00, 0x03, 0x2a) // a monlist request: implementation 3, request code 42
				req = append(req, make([]byte, 40)...)
			}
			reply, e := s.Process(req, dev1, Port, time.Time{})
			answered := mode == ModeClient && version != 0
			if (reply != nil) != answered || (e != nil) != answered {
				t.Errorf("mode %d version %d: reply %v, log entry %v", mode, version, reply != nil, e != nil)
			}
			if reply != nil && len(reply) > len(req) {
				t.Errorf("mode %d: a reply of %d bytes to a request of %d", mode, len(reply), len(req))
			}
		}
	}
	for n := range PacketSize {
		if reply, _ := s.Process(make([]byte, n), dev1, Port, time.Time{}); reply != nil {
			t.Errorf("a request of %d bytes was answered", n)
		}
	}
	// a request with extension fields (an NTS query, a MAC) gets the plain 48 bytes
	long := append(request(4, 5), make([]byte, 200)...)
	if reply, _ := s.Process(long, dev1, Port, time.Time{}); len(reply) != PacketSize {
		t.Errorf("a reply of %d bytes", len(reply))
	}
	if s.Counters().Ignored == 0 {
		t.Error("the ignored datagrams are not counted")
	}
}

func TestEveryAnswerKind(t *testing.T) {
	sinceT := t0.Add(-10 * time.Second)
	abs := time.Date(2038, 1, 19, 3, 9, 7, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		body  string
		check func(t *testing.T, p Packet, e *model.NtpQueryLogEntry, now time.Time)
	}{
		{"offset +400d", `"offset":"+400d"`, func(t *testing.T, p Packet, e *model.NtpQueryLogEntry, now time.Time) {
			want := now.Add(400 * 24 * time.Hour)
			near(t, "transmit", served(p, want), want, time.Microsecond)
			near(t, "receive", p.Receive.Time(want), want, time.Microsecond)
			near(t, "reference", p.Reference.Time(want), want, time.Second)
			if p.Stratum != 2 || p.Leap != 0 || *e.OffsetMs != 400*24*3600*1000 {
				t.Errorf("%+v %d", p, *e.OffsetMs)
			}
		}},
		{"offset -90s", `"offset":"-90s"`, func(t *testing.T, p Packet, e *model.NtpQueryLogEntry, now time.Time) {
			near(t, "transmit", served(p, now), now.Add(-90*time.Second), time.Microsecond)
			if *e.OffsetMs != -90_000 {
				t.Errorf("%d", *e.OffsetMs)
			}
		}},
		{"absolute runs on from the activation", `"absolute":"2038-01-19T03:09:07Z"`, func(t *testing.T, p Packet, e *model.NtpQueryLogEntry, now time.Time) {
			// the fault took effect 10 s ago
			want := abs.Add(10 * time.Second)
			near(t, "transmit", served(p, want), want, time.Microsecond)
			if e.ServedTime == nil || !e.ServedTime.Equal(want) {
				t.Errorf("served time %v", e.ServedTime)
			}
		}},
		{"skew 1000 ppm", `"skew_ppm":1000`, func(t *testing.T, p Packet, e *model.NtpQueryLogEntry, now time.Time) {
			// 10 s since the activation add 10 ms
			near(t, "transmit", served(p, now), now.Add(10*time.Millisecond), time.Microsecond)
			if *e.OffsetMs != 10 {
				t.Errorf("%d ms", *e.OffsetMs)
			}
		}},
		{"skew -1000 ppm", `"skew_ppm":-1000`, func(t *testing.T, p Packet, _ *model.NtpQueryLogEntry, now time.Time) {
			near(t, "transmit", served(p, now), now.Add(-10*time.Millisecond), time.Microsecond)
		}},
		{"skew on top of an offset", `"offset":"+1h","skew_ppm":500`, func(t *testing.T, p Packet, _ *model.NtpQueryLogEntry, now time.Time) {
			want := now.Add(time.Hour + 5*time.Millisecond)
			near(t, "transmit", served(p, want), want, time.Microsecond)
		}},
		{"skew on top of an absolute time", `"absolute":"2038-01-19T03:09:07Z","skew_ppm":100000`, func(t *testing.T, p Packet, _ *model.NtpQueryLogEntry, _ time.Time) {
			want := abs.Add(10*time.Second + time.Second) // 10 s running on, 10% fast
			near(t, "transmit", served(p, want), want, time.Microsecond)
		}},
		{"a clock that stands still", `"absolute":"2038-01-19T03:09:07Z","skew_ppm":-1000000`, func(t *testing.T, p Packet, _ *model.NtpQueryLogEntry, _ time.Time) {
			near(t, "transmit", served(p, abs), abs, time.Microsecond)
		}},
		{"leap insert", `"leap":"insert"`, func(t *testing.T, p Packet, _ *model.NtpQueryLogEntry, now time.Time) {
			near(t, "transmit", served(p, now), now, time.Microsecond)
			if p.Leap != LeapInsert || p.Stratum != 2 {
				t.Errorf("%+v", p)
			}
		}},
		{"leap delete", `"leap":"delete"`, func(t *testing.T, p Packet, _ *model.NtpQueryLogEntry, _ time.Time) {
			if p.Leap != LeapDelete {
				t.Errorf("%+v", p)
			}
		}},
		{"unsynchronized", `"answer":"unsynchronized"`, func(t *testing.T, p Packet, e *model.NtpQueryLogEntry, now time.Time) {
			if p.Leap != LeapUnsynchronized || p.Stratum != 16 || p.Mode != ModeServer || p.RefID != [4]byte{} {
				t.Errorf("%+v", p)
			}
			near(t, "transmit", served(p, now), now, time.Microsecond) // a careless client takes the right time
			if *e.Answer != model.NtpQueryLogEntryAnswerUnsynchronized {
				t.Error(*e.Answer)
			}
		}},
		{"kiss-of-death RATE", `"answer":"kiss_of_death","kiss_code":"RATE"`, func(t *testing.T, p Packet, _ *model.NtpQueryLogEntry, _ time.Time) {
			if p.Leap != LeapUnsynchronized || p.Stratum != 0 || string(p.RefID[:]) != "RATE" || p.Mode != ModeServer {
				t.Errorf("%+v", p)
			}
		}},
		{"kiss-of-death without a code is RATE", `"answer":"kiss_of_death"`, func(t *testing.T, p Packet, _ *model.NtpQueryLogEntry, _ time.Time) {
			// a client told DENY stops using the server for good, one told RATE only slows down: the default must be the mild one
			if p.Stratum != 0 || string(p.RefID[:]) != "RATE" {
				t.Errorf("%+v", p)
			}
		}},
		{"kiss-of-death DENY", `"answer":"kiss_of_death","kiss_code":"DENY"`, func(t *testing.T, p Packet, _ *model.NtpQueryLogEntry, _ time.Time) {
			if p.Stratum != 0 || string(p.RefID[:]) != "DENY" {
				t.Errorf("%+v", p)
			}
		}},
		{"kiss-of-death RSTR", `"answer":"kiss_of_death","kiss_code":"RSTR"`, func(t *testing.T, p Packet, _ *model.NtpQueryLogEntry, _ time.Time) {
			if p.Stratum != 0 || string(p.RefID[:]) != "RSTR" {
				t.Errorf("%+v", p)
			}
		}},
		{"origin mismatch", `"answer":"origin_mismatch"`, func(t *testing.T, p Packet, _ *model.NtpQueryLogEntry, now time.Time) {
			sent := Timestamp(0x1234_5678_9abc_def0)
			// not the timestamp the client sent, not zero (a client could take that for "no origin"), and different in the
			// seconds and in the fraction, so that a client that compares only one of them rejects it too
			if p.Origin == sent || p.Origin == 0 || p.Origin>>32 == sent>>32 || uint32(p.Origin) == uint32(uint64(sent)) || p.Mode != ModeServer || p.Stratum != 2 {
				t.Errorf("%+v", p)
			}
			near(t, "transmit", served(p, now), now, time.Microsecond)
		}},
		{"zero transmit", `"answer":"zero_transmit"`, func(t *testing.T, p Packet, e *model.NtpQueryLogEntry, _ time.Time) {
			if p.Transmit != 0 || p.Origin != 0x1234_5678_9abc_def0 || e.ServedTime != nil {
				t.Errorf("%+v %v", p, e.ServedTime)
			}
		}},
		{"wrong mode", `"answer":"wrong_mode"`, func(t *testing.T, p Packet, _ *model.NtpQueryLogEntry, _ time.Time) {
			if p.Mode == ModeServer {
				t.Errorf("%+v", p)
			}
		}},
		{"answer normal is the correct time", `"answer":"normal","leap":"delete"`, func(t *testing.T, p Packet, _ *model.NtpQueryLogEntry, now time.Time) {
			near(t, "transmit", served(p, now), now, time.Microsecond)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, clk := newServer(t, nil)
			s.SetConfig(config(t, []int{Port}, fault(ovA, Port, sinceT, []string{"10.20.0.11"}, tc.body)))
			p, e, ok := ask(t, s, dev1, Port)
			if !ok {
				t.Fatal("no answer")
			}
			if e.Fault == nil || e.Fault.String() != ovA {
				t.Errorf("the log entry does not name the fault: %v", e.Fault)
			}
			tc.check(t, p, e, clk.Now())
			// the device next to it gets the correct time
			other, _, ok := ask(t, s, dev2, Port)
			if !ok || other.Stratum != 2 || other.Leap != 0 {
				t.Fatalf("a device outside the sources: %+v", other)
			}
			near(t, "the other device's time", served(other, clk.Now()), clk.Now(), time.Microsecond)
		})
	}
}

func TestSilenceLeavesTheQueryUnansweredAndCountsIt(t *testing.T) {
	s, _ := newServer(t, nil)
	s.SetConfig(config(t, []int{Port}, fault(ovA, Port, t0, []string{"10.20.0.0/24"}, `"answer":"silence"`)))
	for range 3 {
		reply, e := s.Process(request(4, 7), dev1, Port, time.Time{})
		if reply != nil {
			t.Fatal("silence was answered")
		}
		if e == nil || *e.Answer != model.NtpQueryLogEntryAnswerSilence || e.Fault == nil || e.ServedTime != nil {
			t.Fatalf("the query is not logged as silenced: %+v", e)
		}
	}
	if c := s.Counters(); c.Silenced != 3 || c.Queries != 3 || c.Answered != 0 {
		t.Errorf("%+v", c)
	}
}

// What the server serves when the sum of the clock and an offset leaves the range an NTP timestamp carries (the
// validation does not bound an offset, because the sum is only known at the moment of the query).
func TestAnOffsetBeyondTheEraOfTheProtocol(t *testing.T) {
	s, clk := newServer(t, nil)
	s.SetConfig(config(t, []int{Port},
		fault(ovA, Port, t0, []string{"10.20.0.11"}, `"offset":"+20y"`),
		fault(ovB, Port, t0, []string{"10.20.0.12"}, `"offset":"-130y"`)))
	// +20y is 2046, in era 1: the seconds wrapped, and a client resolves them from its own clock
	p, e, _ := ask(t, s, dev1, Port)
	want := clk.Now().Add(20 * 365 * 24 * time.Hour)
	if want.Year() != 2046 || p.Transmit.Seconds() >= 1<<31 {
		t.Fatalf("test data: %v, second %d", want, p.Transmit.Seconds())
	}
	near(t, "+20y resolved around the served time", served(p, want), want, time.Microsecond)
	if *e.OffsetMs != 20*365*24*3600*1000 {
		t.Errorf("the offset logged is %d ms", *e.OffsetMs)
	}
	// -130y is 1896, before the epoch: the answer is the first second of 1900
	p, e, _ = ask(t, s, dev2, Port)
	if p.Transmit.Seconds() != 1 || p.Receive.Seconds() != 1 || p.Transmit == 0 {
		t.Errorf("-130y: transmit %#x receive %#x, want the first second of 1900", uint64(p.Transmit), uint64(p.Receive))
	}
	near(t, "the clamped time", e.ServedTime.UTC(), time.Date(1900, 1, 1, 0, 0, 1, 0, time.UTC), 0)
	if *e.OffsetMs >= 0 {
		t.Errorf("the offset logged is %d ms", *e.OffsetMs)
	}
}

func TestTheAnswersAcrossTheEraBoundaryAndTheOverflowOf2038(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start time.Time
		body  string
		want  time.Time
	}{
		{"the NTP era 1 preset", t0, `"absolute":"2036-02-07T06:23:16Z"`, time.Date(2036, 2, 7, 6, 23, 16, 0, time.UTC)},
		{"the 2038 preset", t0, `"absolute":"2038-01-19T03:09:07Z"`, time.Date(2038, 1, 19, 3, 9, 7, 0, time.UTC)},
		{"1970", t0, `"absolute":"1970-01-01T00:00:00Z"`, time.Unix(0, 0)},
		{"the end of era 0 by an offset", time.Date(2036, 2, 7, 6, 28, 0, 0, time.UTC), `"offset":"+30s"`, time.Date(2036, 2, 7, 6, 28, 30, 0, time.UTC)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, clk := newServer(t, nil)
			clk.JumpWall(tc.start.Sub(t0))
			s.SetConfig(config(t, []int{Port}, fault(ovA, Port, clk.Now(), []string{"10.20.0.11"}, tc.body)))
			// the device polls over the next 20 minutes and gets the preset instant plus the time elapsed since the fault began
			for elapsed := time.Duration(0); elapsed < 20*time.Minute; elapsed += 4 * time.Minute {
				clk.Advance(4 * time.Minute)
				p, _, ok := ask(t, s, dev1, Port)
				if !ok {
					t.Fatal("no answer")
				}
				want := tc.want.Add(elapsed + 4*time.Minute)
				if tc.name == "the end of era 0 by an offset" {
					want = clk.Now().Add(30 * time.Second)
				}
				near(t, fmt.Sprintf("after %v", elapsed+4*time.Minute), served(p, want), want, time.Microsecond)
			}
		})
	}
}

// Only the port and the client decide which fault answers: the queries of D36's redirect.
func TestTheFaultIsPickedByTheClientAndThePortTheQueryArrivedOn(t *testing.T) {
	s, clk := newServer(t, nil)
	s.SetConfig(config(t, []int{Port, 12301},
		// the device has a fault for every server (port 123) and a different one for the server on 12301
		fault(ovA, Port, t0, []string{"10.20.0.11"}, `"offset":"+1h"`),
		fault(ovB, 12301, t0, []string{"10.20.0.11"}, `"offset":"-1h"`),
		// the group of the other two devices has a fault only for the server on 12301
		fault("33333333-3333-4333-8333-333333333333", 12301, t0, []string{"10.20.0.12", "10.20.0.13"}, `"answer":"silence"`)))
	check := func(from netip.Addr, port int, want time.Duration, silent bool) {
		t.Helper()
		p, e, ok := ask(t, s, from, port)
		if silent {
			if ok {
				t.Errorf("%v on %d was answered", from, port)
			}
			return
		}
		if !ok {
			t.Fatalf("%v on %d: no answer", from, port)
		}
		near(t, fmt.Sprintf("%v on %d", from, port), served(p, clk.Now().Add(want)), clk.Now().Add(want), time.Microsecond)
		_ = e
	}
	check(dev1, Port, time.Hour, false)
	check(dev1, 12301, -time.Hour, false)
	check(dev2, Port, 0, false)
	check(dev2, 12301, 0, true)
	check(dev3, 12301, 0, true)
	check(netip.MustParseAddr("10.20.0.99"), 12301, 0, false) // no fault: the correct time
	check(dev1, 12399, 0, false)                              // a port without an entry
}

func TestTheServerAskedIsKnownWhereTheSelectorNamesOneAddress(t *testing.T) {
	s, _ := newServer(t, nil)
	s.SetConfig(config(t, []int{Port, 12301, 12302},
		fault(ovA, 12301, t0, []string{"10.20.0.0/24"}, `"offset":"+1h","destination":{"cidr":"216.239.35.0"}`),
		fault(ovB, 12302, t0, []string{"10.20.0.0/24"}, `"offset":"+1h","destination":{"cidr":"216.239.35.0/24"}`)))
	_, e, _ := ask(t, s, dev1, 12301)
	if e.Server == nil || *e.Server != "216.239.35.0:123" {
		t.Errorf("server %v", e.Server)
	}
	_, e, _ = ask(t, s, dev1, 12302)
	if e.Server != nil {
		t.Errorf("a prefix does not name a server: %v", *e.Server)
	}
}

func TestTheFaultStartsAndEndsWithAJumpOfTheConfiguration(t *testing.T) {
	s, clk := newServer(t, nil)
	s.SetConfig(config(t, []int{Port}))
	p, _, _ := ask(t, s, dev1, Port)
	near(t, "before", served(p, clk.Now()), clk.Now(), time.Microsecond)
	clk.Advance(time.Minute)
	s.SetConfig(config(t, []int{Port}, fault(ovA, Port, clk.Now(), []string{"10.20.0.11"}, `"offset":"+400d"`)))
	p, _, _ = ask(t, s, dev1, Port)
	near(t, "during", served(p, clk.Now().Add(400*24*time.Hour)), clk.Now().Add(400*24*time.Hour), time.Microsecond)
	clk.Advance(time.Minute)
	s.SetConfig(config(t, []int{Port}))
	p, _, _ = ask(t, s, dev1, Port)
	near(t, "after", served(p, clk.Now()), clk.Now(), time.Microsecond)
}

// A fault that took effect after the host clock now reads (the clock was stepped back) has not run yet: the time of an
// absolute fault does not go backwards from its instant.
func TestAnAbsoluteFaultDoesNotRunBackwardsWhenTheHostClockIsSteppedBack(t *testing.T) {
	s, clk := newServer(t, nil)
	s.SetConfig(config(t, []int{Port}, fault(ovA, Port, t0, []string{"10.20.0.11"}, `"absolute":"2038-01-19T03:09:07Z","skew_ppm":1000`)))
	clk.JumpWall(-time.Hour)
	p, _, _ := ask(t, s, dev1, Port)
	near(t, "served", served(p, time.Date(2038, 1, 19, 3, 9, 7, 0, time.UTC)), time.Date(2038, 1, 19, 3, 9, 7, 0, time.UTC), time.Microsecond)
}

func TestAnUnsynchronizedHostClockMakesTheServerAnswerStratum16(t *testing.T) {
	unsync := StaticSync{Synchronized: false, EstError: time.Second, MaxError: 5 * time.Second}
	s, clk := newServer(t, func(o *Options) { o.Sync = unsync })
	s.SetConfig(config(t, []int{Port},
		fault(ovA, Port, t0, []string{"10.20.0.11"}, `"offset":"+1h"`),
		fault(ovB, Port, t0, []string{"10.20.0.12"}, `"absolute":"2038-01-19T03:09:07Z"`),
		fault("33333333-3333-4333-8333-333333333333", Port, t0, []string{"10.20.0.13"}, `"answer":"kiss_of_death","kiss_code":"DENY"`)))
	// no fault: leap indicator 3 and stratum 16
	p, e, ok := ask(t, s, netip.MustParseAddr("10.20.0.50"), Port)
	if !ok || p.Leap != LeapUnsynchronized || p.Stratum != 16 || p.Mode != ModeServer || *e.Answer != model.NtpQueryLogEntryAnswerUnsynchronized {
		t.Fatalf("%+v %v", p, e)
	}
	if p.RootDispersion != shortFormat(5*time.Second) {
		t.Errorf("dispersion %#x", p.RootDispersion)
	}
	// a fault built on the host clock is not to be trusted either
	p, _, _ = ask(t, s, dev1, Port)
	if p.Stratum != 16 || p.Leap != LeapUnsynchronized {
		t.Errorf("an offset on an unsynchronized clock: %+v", p)
	}
	// a fault that sets the time itself does not depend on it, nor does a kiss-of-death
	p, _, _ = ask(t, s, dev2, Port)
	want := time.Date(2038, 1, 19, 3, 9, 7, 0, time.UTC).Add(clk.Now().Sub(t0))
	if p.Stratum != 2 || p.Leap != 0 {
		t.Errorf("an absolute time: %+v", p)
	}
	near(t, "absolute", served(p, want), want, time.Microsecond)
	if p, _, _ = ask(t, s, dev3, Port); p.Stratum != 0 || string(p.RefID[:]) != "DENY" {
		t.Errorf("kiss-of-death: %+v", p)
	}
	// the probe says the same
	reply, _ := s.Process(Packet{Version: 4, Mode: ModeClient, Transmit: probeTransmit}.Marshal(), dev1, Port, time.Time{})
	if pp, _ := Parse(reply); pp.Stratum != 16 {
		t.Errorf("the probe: %+v", pp)
	}
}

func TestAReceiveTimeFromTheKernelIsUsedAndTheTransmitTimeIsTakenLast(t *testing.T) {
	s, clk := newServer(t, nil)
	s.SetConfig(config(t, []int{Port}))
	rx := clk.Now().Add(-5 * time.Millisecond) // the datagram waited 5 ms before the server read it
	reply, _ := s.Process(request(4, 1), dev1, Port, rx)
	p, _ := Parse(reply)
	near(t, "receive", p.Receive.Time(rx), rx, time.Microsecond)
	near(t, "transmit", p.Transmit.Time(rx), clk.Now(), time.Microsecond)
	// a receive time in the future of the clock cannot make the transmit time earlier
	rx = clk.Now().Add(time.Second)
	reply, _ = s.Process(request(4, 1), dev1, Port, rx)
	p, _ = Parse(reply)
	if p.Transmit < p.Receive {
		t.Error("an answer that was sent before it was received")
	}
}

func TestADeviceMayAsk16TimesASecondAndTheRestIsDropped(t *testing.T) {
	s, clk := newServer(t, func(o *Options) { o.RateLimit = 0 }) // the default
	s.SetConfig(config(t, []int{Port}))
	answered, droppedEntries := 0, 0
	for range 40 {
		reply, e := s.Process(request(4, 1), dev1, Port, time.Time{})
		if reply != nil {
			answered++
		} else if e != nil {
			droppedEntries++
			if *e.Answer != model.NtpQueryLogEntryAnswerDropped || e.ServedTime != nil {
				t.Errorf("%+v", e)
			}
		}
	}
	if answered != 16 {
		t.Errorf("%d answers in a burst, want 16", answered)
	}
	if droppedEntries != 1 {
		t.Errorf("%d dropped queries in the log, want one", droppedEntries)
	}
	if c := s.Counters(); c.Dropped != 24 || c.Answered != 16 {
		t.Errorf("%+v", c)
	}
	// another device is not affected
	if _, _, ok := ask(t, s, dev2, Port); !ok {
		t.Error("the limit of one device holds back another")
	}
	// a wall clock that jumps does not refill the bucket: the limit counts monotonic time
	clk.JumpWall(time.Hour)
	if _, _, ok := ask(t, s, dev1, Port); ok {
		t.Error("a jump of the wall clock refilled the bucket")
	}
	// half a second later 8 tokens are back
	clk.Advance(500 * time.Millisecond)
	n := 0
	for range 20 {
		if _, _, ok := ask(t, s, dev1, Port); ok {
			n++
		}
	}
	if n != 8 {
		t.Errorf("%d answers after half a second, want 8", n)
	}
	// a drop is logged again after a second
	clk.Advance(time.Second)
	for range 16 {
		ask(t, s, dev1, Port)
	}
	if _, e, ok := ask(t, s, dev1, Port); ok || e == nil {
		t.Errorf("the next drop after a second is not logged: %v", e)
	}
}

func TestTheLimiterTableStaysBounded(t *testing.T) {
	l := newLimiter(16)
	now := time.Duration(0)
	for i := range 3 * limiterEntries {
		a := netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)})
		if ok, _ := l.allow(a, now); !ok {
			t.Fatalf("the first query of %v was dropped", a)
		}
		if len(l.m) > limiterEntries {
			t.Fatalf("%d entries", len(l.m))
		}
		now += time.Microsecond
	}
}

// A flood of distinct source addresses fills the table with fresh buckets, which no sweep can free: the table is walked at most
// once per sweepEvery and not for every new address, since every query of every device takes the same lock (the added time of
// an answer stays under a millisecond, plan §2.18).
func TestAFloodOfNewAddressesDoesNotWalkTheFullTableForEachOfThem(t *testing.T) {
	l := newLimiter(16)
	now := time.Duration(0)
	const n = 5 * limiterEntries
	for i := range n {
		a := netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)})
		l.allow(a, now)
		now += time.Microsecond // all of it within one second: nothing is idle
	}
	if l.sweeps > 2 {
		t.Errorf("the full table was walked %d times for %d new addresses within one second", l.sweeps, n)
	}
	if len(l.m) > limiterEntries {
		t.Errorf("%d entries", len(l.m))
	}
	// a second later the walk is allowed again and frees the devices that are quiet
	now += 2 * time.Second
	before := l.sweeps
	l.allow(netip.MustParseAddr("192.0.2.1"), now)
	if l.sweeps != before+1 || len(l.m) != 1 {
		t.Errorf("a second later: %d sweeps (before %d), %d entries", l.sweeps, before, len(l.m))
	}
}

func TestAnUnparsableFaultIsTreatedAsAnEmptyOne(t *testing.T) {
	// the API validated the fault; a server that meets one it cannot read answers the correct time and does not crash
	s, clk := newServer(t, nil)
	s.SetConfig(config(t, []int{Port}, fault(ovA, Port, t0, []string{"not-an-address", "10.20.0.11"}, `"offset":"garbage","kiss_code":"DENY"`)))
	p, _, ok := ask(t, s, dev1, Port)
	if !ok {
		t.Fatal("no answer")
	}
	near(t, "time", served(p, clk.Now()), clk.Now(), time.Microsecond)
}

func BenchmarkProcess(b *testing.B) {
	s, _ := newServer(b, nil)
	s.SetConfig(config(b, []int{Port}, fault(ovA, Port, t0, []string{"10.20.0.0/24"}, `"offset":"+400d","skew_ppm":100`)))
	req := request(4, 0x1234_5678_9abc_def0)
	b.ReportAllocs()
	for b.Loop() {
		if reply, _ := s.Process(req, dev1, Port, time.Time{}); reply == nil {
			b.Fatal("no answer")
		}
	}
}
