//go:build !linux

package ntpserver

import (
	"net"
	"net/netip"
	"time"
)

// EnableKernelTimestamps has no effect off Linux: the server's clock stamps the datagram.
func EnableKernelTimestamps(net.PacketConn) error { return nil }

const oobSize = 0

func readPacket(pc net.PacketConn, buf, _ []byte) (int, netip.AddrPort, time.Time, error) {
	return readPlain(pc, buf)
}
