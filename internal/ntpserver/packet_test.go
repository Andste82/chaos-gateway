package ntpserver

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestATimestampCountsSecondsSince1900(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   time.Time
		sec  uint32
	}{
		{"the Unix epoch", time.Unix(0, 0), 2208988800},
		{"1900-01-01 00:00:01", time.Date(1900, 1, 1, 0, 0, 1, 0, time.UTC), 1},
		{"2026-11-03", time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC), uint32(time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC).Unix() + ntpToUnix)},
	} {
		if got := TimestampOf(tc.at).Seconds(); got != tc.sec {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.sec)
		}
	}
	if f := TimestampOf(time.Unix(100, 500_000_000)).Fraction(); f != 1<<31 {
		t.Errorf("half a second is the fraction %#x", f)
	}
}

// The seconds of an NTP timestamp have 32 bits and wrap at 2036-02-07 06:28:16 UTC, the end of era 0. A time after that
// is carried as small seconds again, and a client resolves the era from its own clock.
func TestTheTimestampWrapsAtTheEndOfEra0(t *testing.T) {
	end := time.Date(2036, 2, 7, 6, 28, 16, 0, time.UTC)
	if got := TimestampOf(end); got != 0 {
		// second 0 and fraction 0 is the "not set" timestamp: the server never serves exactly this instant by chance
		// that matters, but the arithmetic must be exact
		t.Errorf("the first instant of era 1 is %#x, want 0", uint64(got))
	}
	if got := TimestampOf(end.Add(-time.Second)).Seconds(); got != 0xFFFFFFFF {
		t.Errorf("the last second of era 0 is %#x", got)
	}
	if got := TimestampOf(end.Add(time.Hour)).Seconds(); got != 3600 {
		t.Errorf("an hour into era 1 is second %d", got)
	}
	// a reader whose clock is a little before the end resolves both sides of the boundary
	pivot := end.Add(-5 * time.Minute)
	for _, at := range []time.Time{end.Add(-time.Second), end.Add(time.Nanosecond * 0), end.Add(time.Hour), end.Add(20 * 365 * 24 * time.Hour)} {
		ts := TimestampOf(at)
		if ts == 0 {
			continue
		}
		if got := ts.Time(pivot); got.Sub(at) > time.Nanosecond || at.Sub(got) > time.Nanosecond {
			t.Errorf("%v resolves to %v", at, got)
		}
	}
	// and a reader in 2026 resolves a timestamp of era 0 to 2026 and one that looks like era 1 to the nearest instant
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	if got := TimestampOf(now).Time(now); !got.Equal(now) {
		t.Errorf("2026 resolves to %v", got)
	}
}

func TestTheTimestampSurvivesTheSigned32BitOverflowOf2038(t *testing.T) {
	// 2038-01-19 03:14:07 UTC is the last second a signed 32-bit time_t holds
	last := time.Date(2038, 1, 19, 3, 14, 7, 0, time.UTC)
	if last.Unix() != 1<<31-1 {
		t.Fatalf("test data: %d", last.Unix())
	}
	for _, at := range []time.Time{last.Add(-time.Second), last, last.Add(time.Second), last.Add(24 * time.Hour)} {
		ts := TimestampOf(at)
		if want := uint32(at.Unix() + ntpToUnix); ts.Seconds() != want {
			t.Errorf("%v: second %d, want %d", at, ts.Seconds(), want)
		}
		if got := ts.Time(at); !got.Equal(at) {
			t.Errorf("%v resolves to %v", at, got)
		}
	}
	if TimestampOf(last.Add(time.Second)).Seconds() <= TimestampOf(last).Seconds() {
		t.Error("the second after the overflow is not later")
	}
}

func TestATimeBefore1900IsClampedAndZeroIsNeverServed(t *testing.T) {
	for _, at := range []time.Time{time.Date(1899, 12, 31, 23, 59, 59, 0, time.UTC), time.Date(1850, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if got := TimestampOf(at); got.Seconds() != 1 || got.Fraction() != 0 {
			t.Errorf("%v: %#x", at, uint64(got))
		}
	}
	if !Timestamp(0).Time(t0).IsZero() {
		t.Error("a timestamp of 0 is not a time")
	}
}

func TestTheFractionIsExactToAboutANanosecond(t *testing.T) {
	for _, ns := range []int{0, 1, 999, 1_000_000, 123_456_789, 999_999_999} {
		at := time.Date(2026, 1, 1, 0, 0, 0, ns, time.UTC)
		got := TimestampOf(at).Time(at)
		if d := got.Sub(at); d > 2 || d < -2 {
			t.Errorf("%d ns comes back as %v", ns, d)
		}
	}
}

func TestAPacketIsParsedAndWrittenFieldByField(t *testing.T) {
	p := Packet{Leap: 3, Version: 4, Mode: 4, Stratum: 2, Poll: -6, Precision: -20, RootDelay: 0x00010002, RootDispersion: 0x00030004,
		RefID: [4]byte{10, 20, 0, 1}, Reference: 1, Origin: 2, Receive: 3, Transmit: 0xFFFF_FFFF_FFFF_FFFF}
	b := p.Marshal()
	if len(b) != PacketSize || b[0] != 3<<6|4<<3|4 {
		t.Fatalf("% x", b)
	}
	got, err := Parse(b)
	if err != nil || got != p {
		t.Fatalf("%+v %v, want %+v", got, err, p)
	}
	// extension fields and a MAC after the header do not change what is read
	got, err = Parse(append(b, bytes.Repeat([]byte{0xAA}, 40)...))
	if err != nil || got != p {
		t.Fatalf("with a trailer: %+v %v", got, err)
	}
}

func TestADatagramShorterThanTheHeaderIsNoPacket(t *testing.T) {
	for n := range PacketSize {
		if _, err := Parse(make([]byte, n)); !errors.Is(err, ErrShort) {
			t.Errorf("%d bytes: %v", n, err)
		}
	}
}

func TestTheRootDelayAndDispersionAreSixteenDotSixteenSeconds(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want uint32
	}{
		{0, 0}, {-time.Second, 0}, {time.Second, 1 << 16}, {500 * time.Millisecond, 1 << 15}, {1500 * time.Microsecond, 98},
		{65535 * time.Second, 65535 << 16}, {100000 * time.Second, 0xffffffff},
	} {
		if got := shortFormat(tc.d); got != tc.want {
			t.Errorf("%v: %#x, want %#x", tc.d, got, tc.want)
		}
	}
}
