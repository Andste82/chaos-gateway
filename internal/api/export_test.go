package api

import (
	"context"
	"time"
)

// SetStreamTimings shortens the write timeout and the keepalive of the event stream for tests and
// returns a function that restores them.
func SetStreamTimings(write, keep time.Duration) (restore func()) {
	w, k := writeTimeout, keepalive
	writeTimeout, keepalive = write, keep
	return func() { writeTimeout, keepalive = w, k }
}

// SetDNSPoll shortens the DNS configuration's long poll for tests and returns a function that
// restores it.
func SetDNSPoll(wait, tick time.Duration) (restore func()) {
	old := dnsPoll
	dnsPoll.wait, dnsPoll.tick = wait, tick
	return func() { dnsPoll = old }
}

// SetSysClassNet points the reads of interface state and counters at dir and returns a function
// that restores them.
func SetSysClassNet(dir string) (restore func()) {
	old := sysClassNet
	sysClassNet = dir
	return func() { sysClassNet = old }
}

// OSConfigCacheReadsOfALateCaller fills the cache of GET /setup's read of the host's network configuration,
// then loads as a caller does that found the cache stale just before that read filled it, and returns how
// many reads were made (the Phase 3 audit's cache: one).
func OSConfigCacheReadsOfALateCaller() int {
	var c osConfigCache
	reads := 0
	read := func(context.Context) osConfig { reads++; return osConfig{} }
	now := time.Now()
	c.get(context.Background(), now, read)
	c.load(context.Background(), now, read)
	return reads
}
