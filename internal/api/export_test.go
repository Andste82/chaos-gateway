package api

import "time"

// SetStreamTimings shortens the write timeout and the keepalive of the event stream for tests and
// returns a function that restores them.
func SetStreamTimings(write, keep time.Duration) (restore func()) {
	w, k := writeTimeout, keepalive
	writeTimeout, keepalive = write, keep
	return func() { writeTimeout, keepalive = w, k }
}
