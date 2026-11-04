//go:build !linux

package ntpserver

import "errors"

// ReadHostSync needs adjtimex, which only Linux has.
func ReadHostSync() (Sync, error) {
	return Sync{}, errors.New("adjtimex needs Linux")
}
