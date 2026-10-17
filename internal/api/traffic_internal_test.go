package api

import (
	"testing"
	"time"
)

// Issue #63: the history of the uplink's rates. Two readings make a rate, oldest first; a reading
// with a lower counter (re-created interface, wrap), of another interface or without counters gives
// none and starts the next interval; a sample leaves after the period.
func TestTheUplinkTrafficHistoryTurnsReadingsIntoRates(t *testing.T) {
	u := &uplinkTraffic{interval: 5 * time.Second, period: 20 * time.Second}
	wall := time.Date(2026, 10, 17, 12, 0, 0, 123_456_789, time.UTC)
	at := func(s int) (time.Time, time.Duration) {
		return wall.Add(time.Duration(s) * time.Second), time.Duration(s) * time.Second
	}
	rec := func(s int, name string, rx, tx int64, ok bool) {
		w, m := at(s)
		u.record(name, rx, tx, ok, w, m)
	}

	rec(0, "eth0", 1000, 500, true)
	if v := u.view(nil); len(v.Samples) != 0 || !v.Counters || v.Interface == nil || *v.Interface != "eth0" {
		t.Fatalf("one reading is no rate: %+v", v)
	}
	rec(5, "eth0", 1000+125_000, 500+12_500, true)
	v := u.view(nil)
	if len(v.Samples) != 1 || v.Samples[0].DownBps != 200_000 || v.Samples[0].UpBps != 20_000 {
		t.Fatalf("%+v", v.Samples)
	}
	if want := wall.Add(5 * time.Second).Truncate(time.Millisecond); !v.Samples[0].Time.Equal(want) {
		t.Errorf("the sample's time is %v, want %v (milliseconds)", v.Samples[0].Time, want)
	}
	if v.IntervalSeconds != 5 || v.PeriodSeconds != 20 {
		t.Errorf("%+v", v)
	}

	// a counter that went down (re-created interface, or a wrap): no rate for that interval, the
	// next one counts from the low reading
	rec(10, "eth0", 10, 600_000, true)
	rec(15, "eth0", 10+6250, 600_000, true)
	if v := u.view(nil); len(v.Samples) != 2 || v.Samples[1].DownBps != 10_000 || v.Samples[1].UpBps != 0 {
		t.Fatalf("after the reset: %+v", v.Samples)
	}

	// another uplink interface: its counters are not comparable with the old one's
	rec(17, "eth1", 999_999_999, 999_999_999, true)
	if v := u.view(nil); len(v.Samples) != 2 || *v.Interface != "eth1" {
		t.Fatalf("a new uplink: %+v", v)
	}

	// no counters: said so, and the next reading with counters starts afresh
	rec(19, "eth1", 0, 0, false)
	if v := u.view(nil); v.Counters {
		t.Errorf("%+v", v)
	}
	rec(21, "eth1", 5, 5, true)
	if v := u.view(nil); len(v.Samples) != 2 {
		t.Fatalf("a reading after one without counters is no rate: %+v", v.Samples)
	}

	// eviction: at 26 s the sample of 5 s is older than the 20 s period, the one of 15 s is not
	rec(26, "eth1", 5+1000, 5, true)
	v = u.view(nil)
	if len(v.Samples) != 2 {
		t.Fatalf("%+v", v.Samples)
	}
	if w, _ := at(15); !v.Samples[0].Time.Equal(w.Truncate(time.Millisecond)) {
		t.Errorf("the oldest kept sample is %v", v.Samples[0].Time)
	}

	// since: only what came after the newest sample a client has
	if s := u.view(&v.Samples[0].Time).Samples; len(s) != 1 || !s[0].Time.Equal(v.Samples[1].Time) {
		t.Errorf("since the first: %+v", s)
	}
	if s := u.view(&v.Samples[1].Time).Samples; s == nil || len(s) != 0 {
		t.Errorf("since the newest: %#v", s)
	}
}

// Issue #63: eviction follows the monotonic clock, not the wall clock, which may jump at the first
// NTP sync.
func TestTheUplinkTrafficHistoryEvictsByTheMonotonicClock(t *testing.T) {
	u := &uplinkTraffic{interval: time.Second, period: 10 * time.Second}
	wall := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	u.record("eth0", 0, 0, true, wall, 0)
	u.record("eth0", 100, 100, true, wall.Add(time.Second), time.Second)
	// the wall clock jumps by a year, the monotonic one by a second
	u.record("eth0", 200, 200, true, wall.AddDate(1, 0, 0), 2*time.Second)
	if n := len(u.view(nil).Samples); n != 2 {
		t.Errorf("%d samples after a jump of the wall clock", n)
	}
}
