package ntpserver

import (
	"runtime"
	"testing"
	"time"
)

// The decision of the plan §2.18: unsynchronized when adjtimex reports STA_UNSYNC (or the clock state TIME_ERROR), or the
// maximum error exceeds one second.
func TestTheHostClockIsSynchronizedOnlyWhenTheKernelSaysSo(t *testing.T) {
	for _, tc := range []struct {
		name               string
		state              int
		status             int32
		esterror, maxerror int64
		want               bool
	}{
		{"disciplined", 0, 0x2001, 500, 1000, true},
		{"STA_UNSYNC", 0, staUnsync | 0x2001, 500, 1000, false},
		{"TIME_ERROR", clockErr, 0, 500, 1000, false},
		{"the error is just one second", 0, 0, 0, 1_000_000, true},
		{"the error exceeds one second", 0, 0, 0, 1_000_001, false},
		{"the default after boot: 16 s of maximum error", 0, staUnsync, 16_000_000, 16_000_000, false},
	} {
		got := SyncFromTimex(tc.state, tc.status, tc.esterror, tc.maxerror)
		if got.Synchronized != tc.want {
			t.Errorf("%s: %+v", tc.name, got)
		}
		if got.EstError != time.Duration(tc.esterror)*time.Microsecond || got.MaxError != time.Duration(tc.maxerror)*time.Microsecond {
			t.Errorf("%s: errors %v %v", tc.name, got.EstError, got.MaxError)
		}
	}
}

func TestTheHostClockCanBeReadWithoutAPrivilege(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("adjtimex")
	}
	s, err := ReadHostSync()
	if err != nil {
		t.Fatal(err)
	}
	// the CI container's clock is mostly not synchronized: the value is the host's, only that it was read is asserted
	t.Logf("host clock: %+v", s)
	if s.MaxError < 0 || s.EstError < 0 {
		t.Errorf("%+v", s)
	}
}
