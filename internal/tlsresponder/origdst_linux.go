//go:build linux

package tlsresponder

import (
	"errors"
	"net"
	"net/netip"
	"syscall"
	"unsafe"
)

// soOriginalDst is SO_ORIGINAL_DST of netfilter: the address a connection that was redirected (or translated)
// was meant for, read from its conntrack entry.
const soOriginalDst = 80

// OriginalDst returns the destination a redirected connection was meant for (plan §3.3: the packets arrive in the
// service namespace unchanged, nftables redirects them to the responder's port, and the conntrack entry of the
// namespace remembers where they were going). IPv4 only, as the networks of the gateway are.
func OriginalDst(c net.Conn) (netip.AddrPort, error) {
	tc, ok := c.(*net.TCPConn)
	if !ok {
		return netip.AddrPort{}, errors.New("not a TCP connection")
	}
	raw, err := tc.SyscallConn()
	if err != nil {
		return netip.AddrPort{}, err
	}
	var addr syscall.RawSockaddrInet4
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		size := uint32(unsafe.Sizeof(addr))
		_, _, e := syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, syscall.SOL_IP, soOriginalDst, uintptr(unsafe.Pointer(&addr)), uintptr(unsafe.Pointer(&size)), 0)
		if e != 0 {
			sockErr = e
		}
	}); err != nil {
		return netip.AddrPort{}, err
	}
	if sockErr != nil {
		return netip.AddrPort{}, sockErr
	}
	port := (*[2]byte)(unsafe.Pointer(&addr.Port)) // network byte order
	return netip.AddrPortFrom(netip.AddrFrom4(addr.Addr), uint16(port[0])<<8|uint16(port[1])), nil
}
