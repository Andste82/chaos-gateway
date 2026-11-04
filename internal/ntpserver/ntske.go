package ntpserver

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// NTSKEPort is the TCP port of NTS key establishment (RFC 8915).
const NTSKEPort = 4460

// ntskeInterval is how often one device's NTS-KE connections show in the query log: the event is once per device, and
// a client that retries must not fill the log.
const ntskeInterval = time.Minute

// ServeNTSKE accepts the connections of devices that use NTS (RFC 8915) and closes them: the server cannot answer
// authenticated NTP, because the key exchange is bound to the identity of the server the device asked for, and a third
// party cannot be that (plan §2.18). The connection is reported as a log entry of the kind `nts_ke`, so that the API can
// say once per device that its time does not come through the server. It returns when ctx ends.
func (s *Server) ServeNTSKE(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		var client netip.Addr
		if ta, ok := c.RemoteAddr().(*net.TCPAddr); ok {
			client = ta.AddrPort().Addr().Unmap()
		}
		// whether the server has a configuration is settled before the connection is closed: the peer that sees the close then
		// knows that a configuration it sets afterwards does not count this connection
		configured := s.parsed.Load() != nil
		_ = c.Close()
		if !client.IsValid() || !configured {
			continue
		}
		s.ntske.Add(1)
		s.reportNTS(client, NTSKEPort)
	}
}

// reportNTS puts a device's NTS into the query log, at most once a minute per device: its key-establishment connection to
// port 4460, or a query with NTS extension fields on the UDP port port (a device whose NTS-KE connection went to the real
// server because its scope redirects no hardcoded servers, but whose NTP queries are redirected for a time fault). Both
// say that the device's time does not come through this server (plan §2.18, risk 37).
func (s *Server) reportNTS(client netip.Addr, port int) {
	now := s.opt.Clock.Monotonic()
	s.ntsMu.Lock()
	last, known := s.ntsSeen[client]
	report := !known || now-last >= ntskeInterval
	if report {
		if s.ntsSeen == nil || len(s.ntsSeen) >= limiterEntries {
			s.ntsSeen = map[netip.Addr]time.Duration{}
		}
		s.ntsSeen[client] = now
	}
	s.ntsMu.Unlock()
	if report {
		kind := model.NtsKe
		s.log.add(model.NtpQueryLogEntry{Time: s.opt.Clock.Now(), Client: client.String(), Port: port, Kind: &kind})
	}
}
