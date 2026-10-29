package api

import (
	"context"
	"sort"
	"time"
)

// SafeModeHolds are the ids of the operations that safe mode refuses (plan §3.9).
func SafeModeHolds() []string {
	var ids []string
	for id := range safeModeHolds {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

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
	old := sysClassNet.Swap(&dir)
	return func() { sysClassNet.Store(old) }
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

// SetTLSPoll shortens the TLS configuration's long poll for tests and returns a function that restores it.
func SetTLSPoll(wait, tick time.Duration) (restore func()) {
	old := tlsPoll
	tlsPoll.wait, tlsPoll.tick = wait, tick
	return func() { tlsPoll = old }
}

// TLSHandshakeOutcomes are the outcomes of the handshakes the server has logged for a client address, oldest first: what the checks of the
// runs read.
func TLSHandshakeOutcomes(s *Server, client string) []string {
	s.tls.logMu.Lock()
	defer s.tls.logMu.Unlock()
	var out []string
	for _, h := range s.tls.entries {
		if h.report.Client == client {
			out = append(out, string(h.report.Outcome))
		}
	}
	return out
}
