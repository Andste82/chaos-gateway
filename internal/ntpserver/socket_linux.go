//go:build linux

package ntpserver

import (
	"encoding/binary"
	"net"
	"net/netip"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// EnableKernelTimestamps asks the kernel to stamp each datagram of the socket with the time it received it
// (SO_TIMESTAMPNS), so that the receive timestamp of an answer does not include the time the datagram waited for the
// server to be scheduled (plan §2.18).
func EnableKernelTimestamps(pc net.PacketConn) error {
	uc, ok := pc.(*net.UDPConn)
	if !ok {
		return nil // a socket of a testbed or a test: the server's clock is used
	}
	rc, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := rc.Control(func(fd uintptr) { serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_TIMESTAMPNS, 1) }); err != nil {
		return err
	}
	return serr
}

// oobSize holds the one control message with the timestamp.
const oobSize = 64

// readPacket reads a datagram with the sender and, where the socket stamps them, the kernel's receive time (the zero
// time otherwise).
func readPacket(pc net.PacketConn, buf, oob []byte) (n int, from netip.AddrPort, rx time.Time, err error) {
	uc, ok := pc.(*net.UDPConn)
	if !ok {
		return readPlain(pc, buf)
	}
	n, oobn, _, ap, err := uc.ReadMsgUDPAddrPort(buf, oob)
	if err != nil {
		return 0, netip.AddrPort{}, time.Time{}, err
	}
	if oobn > 0 {
		rx = kernelTime(oob[:oobn])
	}
	return n, netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()), rx, nil
}

// kernelTime finds the SCM_TIMESTAMPNS message in the control data.
func kernelTime(oob []byte) time.Time {
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return time.Time{}
	}
	for _, m := range msgs {
		// struct timespec of a 64-bit kernel: seconds and nanoseconds, each 8 bytes in the machine's byte order
		if m.Header.Level == unix.SOL_SOCKET && m.Header.Type == unix.SO_TIMESTAMPNS && len(m.Data) >= 16 {
			sec := int64(binary.NativeEndian.Uint64(m.Data[0:8]))
			nsec := int64(binary.NativeEndian.Uint64(m.Data[8:16]))
			if nsec < 0 || nsec >= 1_000_000_000 {
				return time.Time{}
			}
			return time.Unix(sec, nsec)
		}
	}
	return time.Time{}
}
