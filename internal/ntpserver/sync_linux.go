//go:build linux

package ntpserver

import (
	"golang.org/x/sys/unix"
)

// ReadHostSync reads the state of the host clock with adjtimex without changing it (modes 0 needs no privilege).
func ReadHostSync() (Sync, error) {
	var tx unix.Timex
	state, err := unix.Adjtimex(&tx)
	if err != nil {
		return Sync{}, err
	}
	return SyncFromTimex(state, tx.Status, tx.Esterror, tx.Maxerror), nil
}
