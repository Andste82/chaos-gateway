package api

import (
	"context"
	"crypto/tls"
	"net"
	"sort"
	"time"

	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/run"
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

// SetNTPPoll shortens the NTP configuration's long poll for tests and returns a function that restores it.
func SetNTPPoll(wait, tick time.Duration) (restore func()) {
	old := ntpPoll
	ntpPoll.wait, ntpPoll.tick = wait, tick
	return func() { ntpPoll = old }
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

// ServedFor returns the certificate that c presents for a handshake that came in on the local address local.
func ServedFor(c *Certificates, local net.Addr) (*tls.Certificate, error) {
	return c.serve(&tls.ClientHelloInfo{Conn: fakeConn{local: local}})
}

// BuiltinIPs are the addresses on which the gateway presents its own certificate.
func BuiltinIPs() []net.IP { return builtinIPs() }

// fakeConn is a connection that only knows its local address.
type fakeConn struct {
	net.Conn
	local net.Addr
}

func (f fakeConn) LocalAddr() net.Addr { return f.local }

// NTPDevice is what the API knows of a device's use of the NTP server (the per-device state of M34).
type NTPDevice = ntpDevice

// NTPDeviceState returns the state of a device (UUID), false for a device that never asked the NTP server.
func (s *Server) NTPDeviceState(device string) (NTPDevice, bool) { return s.ntpDeviceState(device) }

// NTPNetworksOf lists the networks of the server's configuration for a snapshot, as "network gateway clients" lines.
func NTPNetworksOf(snap *engine.Snapshot) []string {
	var out []string
	for _, n := range ntpNetworks(snap) {
		out = append(out, n.Network+" "+n.Gateway+" "+n.Clients)
	}
	return out
}

// NTPLog returns the query log the API keeps, oldest first, with the device the API found for each entry.
func (s *Server) NTPLog() []model.NtpQueryLogEntry {
	s.ntp.logMu.Lock()
	defer s.ntp.logMu.Unlock()
	out := make([]model.NtpQueryLogEntry, len(s.ntp.entries))
	for i, l := range s.ntp.entries {
		out[i] = l.e
	}
	return out
}

// NTPQueriesOf returns the queries of the devices at or after the time, as the wait and the check of a run read them.
func (s *Server) NTPQueriesOf(devices []string, since time.Time) []model.NtpQueryLogEntry {
	var out []model.NtpQueryLogEntry
	for _, l := range s.ntpQueries(devices, since) {
		out = append(out, l.e)
	}
	return out
}

// NTPObserverQueries is what the runs read of the NTP log through the observer interface (run.Observer.NTPQueries).
func (s *Server) NTPObserverQueries(devices []string, since time.Time) []run.NTPQuery {
	return runObserver{s}.NTPQueries(devices, since)
}

// NTPRetained counts the per-device states the API keeps and the entries of their rings.
func (s *Server) NTPRetained() (states, recent int) {
	s.ntp.logMu.Lock()
	defer s.ntp.logMu.Unlock()
	for _, d := range s.ntp.devices {
		states++
		recent += len(d.recent)
	}
	return states, recent
}
