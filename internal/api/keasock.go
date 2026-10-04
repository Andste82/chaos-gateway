package api

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/kea"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// maxKeaDatagram bounds one lease event from the hook's datagram socket (M6a-09).
const maxKeaDatagram = 4096

// ListenKeaEvents serves the Unix datagram socket chaosgw kea-hook writes lease events to, instead
// of forking a process and a TLS handshake per lease (M6a-09): a DHCP flood from an untrusted
// device no longer costs the API more than reading a few more bytes. The hook already keeps
// POST /internal/dhcp/lease-events (PostLeaseEvent) as a fallback for whenever this socket is
// missing or full.
//
// A datagram socket is inherently non-blocking for the writers that matter here: the kernel queues
// up to the socket's receive buffer and drops the rest instead of making a slow write block, so one
// reader, however busy, never backs up the hook. It runs until ctx ends or the socket errors.
func (s *Server) ListenKeaEvents(ctx context.Context, path string) error {
	_ = os.Remove(path)
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return err
	}
	// group-writable so the Kea container (a different uid, sharing the volume) can post to it;
	// the directory it lives in is the service token's, already restricted to the containers that
	// need it (M6a-21)
	_ = os.Chmod(path, 0o660)
	context.AfterFunc(ctx, func() { _ = conn.Close() })
	buf := make([]byte, maxKeaDatagram)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		s.handleKeaDatagram(buf[:n])
	}
}

// handleKeaDatagram decodes and validates one line the same way PostLeaseEvent does; a malformed
// or invalid datagram (there is no client to answer) is simply dropped.
func (s *Server) handleKeaDatagram(raw []byte) {
	var body struct {
		ClientID      string `json:"client_id"`
		Event         string `json:"event"`
		Hostname      string `json:"hostname"`
		IP            string `json:"ip"`
		MAC           string `json:"mac"`
		SubnetID      int    `json:"subnet_id"`
		ValidLifetime int    `json:"valid_lifetime"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return
	}
	ip, err := netip.ParseAddr(body.IP)
	if _, merr := net.ParseMAC(body.MAC); merr != nil || len(body.Hostname) > 253 || err != nil || !ip.Is4() || body.SubnetID <= 0 || !model.KeaLeaseEventEvent(body.Event).Valid() {
		return
	}
	s.cfg.Engine.LeaseEvent(kea.Event{Name: body.Event, IP: ip, MAC: strings.ToLower(body.MAC), ClientID: body.ClientID, Hostname: body.Hostname,
		SubnetID: body.SubnetID, ValidLifetime: body.ValidLifetime})
}
