package ntpserver

import (
	"net"
	"net/netip"
	"strconv"
	"time"
)

// readPlain reads a datagram from any packet connection (the sockets of the testbeds are made by their own listener).
func readPlain(pc net.PacketConn, buf []byte) (int, netip.AddrPort, time.Time, error) {
	n, addr, err := pc.ReadFrom(buf)
	if err != nil {
		return 0, netip.AddrPort{}, time.Time{}, err
	}
	var from netip.AddrPort
	switch a := addr.(type) {
	case *net.UDPAddr:
		from = a.AddrPort()
	default:
		from, _ = netip.ParseAddrPort(addr.String())
	}
	return n, netip.AddrPortFrom(from.Addr().Unmap(), from.Port()), time.Time{}, nil
}

// ListenFunc opens the UDP socket of one port of the server. The server asks for the port 123 and for every redirect
// port of the configuration (decision D36), and closes the ones the configuration no longer names.
type ListenFunc func(port int) (net.PacketConn, error)

// ListenUDP is the production ListenFunc: IPv4 UDP on host (empty for every address of the namespace) with the kernel's
// receive time stamps.
func ListenUDP(host string) ListenFunc {
	return func(port int) (net.PacketConn, error) {
		pc, err := net.ListenPacket("udp4", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			return nil, err
		}
		if err := EnableKernelTimestamps(pc); err != nil {
			_ = pc.Close()
			return nil, err
		}
		return pc, nil
	}
}
