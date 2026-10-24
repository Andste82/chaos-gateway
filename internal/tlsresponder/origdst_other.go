//go:build !linux

package tlsresponder

import (
	"errors"
	"net"
	"net/netip"
)

// OriginalDst is only available on Linux, where the redirect is made.
func OriginalDst(net.Conn) (netip.AddrPort, error) {
	return netip.AddrPort{}, errors.New("SO_ORIGINAL_DST needs Linux")
}
