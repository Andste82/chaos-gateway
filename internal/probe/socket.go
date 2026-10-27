package probe

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/Andste82/chaos-gateway/internal/diag"
)

// udpSocket is the DHCP client's socket on a real interface.
type udpSocket struct{ pc net.PacketConn }

// ListenDHCP opens UDP port 68 on all addresses of the interface ifname inside the network namespace ns (empty: the
// caller's own), bound to the interface and allowed to broadcast, so the client can send and receive before it has an
// address (the server answers by broadcast because the client's messages ask for it). The process needs CAP_NET_RAW (the
// executor is privileged).
func ListenDHCP(ns, ifname string) (Socket, error) {
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			serr = errors.Join(
				unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1),
				unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BROADCAST, 1),
				unix.BindToDevice(int(fd), ifname),
			)
		}); err != nil {
			return err
		}
		return serr
	}}
	var pc net.PacketConn
	err := diag.InNamespace(ns, func() (err error) {
		pc, err = lc.ListenPacket(context.Background(), "udp4", "0.0.0.0:68")
		return
	})
	if err != nil {
		return nil, err
	}
	return &udpSocket{pc: pc}, nil
}

func (s *udpSocket) Send(pkt []byte, dst netip.AddrPort) error {
	_, err := s.pc.WriteTo(pkt, net.UDPAddrFromAddrPort(dst))
	return err
}

func (s *udpSocket) Recv() ([]byte, error) {
	buf := make([]byte, 1500)
	n, _, err := s.pc.ReadFrom(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

func (s *udpSocket) Close() error { return s.pc.Close() }
