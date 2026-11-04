package ntpserver

import "time"

// MaxSyncError is the largest maximum error of the host clock (adjtimex) for which the server still calls the clock
// synchronized (plan §2.18): beyond it the host has not heard from its time source for a long while, whatever the status says.
const MaxSyncError = time.Second

// Sync is what the server knows about how good the host clock is.
type Sync struct {
	// Synchronized is false while the host clock is not disciplined by a time source or its error is too large; the
	// server then answers with leap indicator 3 and stratum 16, so that a correct client ignores it.
	Synchronized bool
	// EstError and MaxError are the kernel's estimated and maximum error of the clock; they become the root delay
	// and the root dispersion of an answer.
	EstError, MaxError time.Duration
}

// SyncState tells whether the host clock is synchronized. Production reads adjtimex; the testbeds, whose hosts are not
// synchronized (CI containers and VMs report STA_UNSYNC), set the answer, and the server answers stratum 16 only when
// the interface says so.
type SyncState interface {
	State() Sync
}

// StaticSync is a SyncState that always says the same.
type StaticSync Sync

// State implements SyncState.
func (s StaticSync) State() Sync { return Sync(s) }

// Synchronized is a host clock that is as good as it can be: what the testbeds use.
var Synchronized = StaticSync{Synchronized: true, EstError: 100 * time.Microsecond, MaxError: 500 * time.Microsecond}

// HostSync reads the state of the host's clock from the kernel (adjtimex).
type HostSync struct{}

// State implements SyncState. A clock that cannot be read counts as not synchronized: the server never claims a time
// it does not have.
func (HostSync) State() Sync {
	s, err := ReadHostSync()
	if err != nil {
		return Sync{}
	}
	return s
}

// Timex status bits and clock states of the kernel (<linux/timex.h>), kept here so that the decision is testable
// without the system call.
const (
	staUnsync = 0x0040
	clockErr  = 5 // TIME_ERROR: the clock is not synchronized
)

// SyncFromTimex decides from what adjtimex returned: the clock state, the status word and the errors in
// microseconds.
func SyncFromTimex(state int, status int32, esterror, maxerror int64) Sync {
	s := Sync{EstError: time.Duration(esterror) * time.Microsecond, MaxError: time.Duration(maxerror) * time.Microsecond}
	s.Synchronized = state != clockErr && status&staUnsync == 0 && s.MaxError <= MaxSyncError
	return s
}
