package ntpserver

import (
	"errors"
	"fmt"
	"syscall"
	"testing"
)

// The arm64 unit tests run under qemu-user, which does not know SO_TIMESTAMPNS (ENOPROTOOPT): the listener must go on without the
// kernel's receive time then, and stop for every other error.
func TestAnEnvironmentWithoutKernelTimestampsStillServesAndOtherErrorsStop(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"the option is unknown":              {syscall.ENOPROTOOPT, true},
		"the option is unknown, wrapped":     {fmt.Errorf("set SO_TIMESTAMPNS: %w", syscall.ENOPROTOOPT), true},
		"no permission":                      {syscall.EPERM, false},
		"a closed socket":                    {syscall.EBADF, false},
		"another error":                      {errors.New("boom"), false},
		"the operation is not supported too": {syscall.EOPNOTSUPP, false},
	} {
		if got := timestampsUnavailable(tc.err); got != tc.want {
			t.Errorf("%s: timestampsUnavailable(%v) = %v, want %v", name, tc.err, got, tc.want)
		}
	}
}
