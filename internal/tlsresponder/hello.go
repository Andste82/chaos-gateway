package tlsresponder

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"time"
)

// A TLS record starts with five bytes: the type (22, handshake), the version and the length of the fragment.
const (
	recordHeader    = 5
	recordHandshake = 22
	handshakeHello  = 1
	// maxHello bounds what the responder reads before it knows what to do: a ClientHello is a few hundred bytes (a
	// post-quantum key share makes it about 1.5 kB), and may be split over records.
	maxHello = 64 << 10
	// maxRecord is the largest fragment of a record (RFC 8446: 2^14).
	maxRecord = 1 << 14
)

var errNotTLS = errors.New("not a TLS ClientHello")

// readClientHello reads the first handshake message of a connection, a ClientHello, as the bytes the client sent (the
// records, with their headers: they are replayed to the TLS stack or to the destination of a connection that is passed
// through) and the server name in it, empty when it has none. The read ends at the deadline. A connection that does not
// begin with a ClientHello returns what was read and errNotTLS.
func readClientHello(c net.Conn, deadline time.Time) (raw []byte, sni string, err error) {
	if err := c.SetReadDeadline(deadline); err != nil {
		return nil, "", err
	}
	defer func() { _ = c.SetReadDeadline(time.Time{}) }()
	var msg []byte // the handshake message, the fragments of the records put together
	for {
		hdr := make([]byte, recordHeader)
		n, err := io.ReadFull(c, hdr)
		raw = append(raw, hdr[:n]...)
		if err != nil {
			return raw, "", err
		}
		if hdr[0] != recordHandshake || hdr[1] != 3 {
			return raw, "", errNotTLS
		}
		length := int(binary.BigEndian.Uint16(hdr[3:5]))
		if length == 0 || length > maxRecord {
			return raw, "", errNotTLS
		}
		frag := make([]byte, length)
		n, err = io.ReadFull(c, frag)
		raw = append(raw, frag[:n]...)
		if err != nil {
			return raw, "", err
		}
		msg = append(msg, frag...)
		if len(msg) >= 4 {
			if msg[0] != handshakeHello {
				return raw, "", errNotTLS
			}
			want := int(msg[1])<<16 | int(msg[2])<<8 | int(msg[3])
			if want > maxHello {
				return raw, "", errNotTLS
			}
			if len(msg) >= 4+want {
				return raw, serverName(msg[4 : 4+want]), nil
			}
		}
		if len(raw) > maxHello {
			return raw, "", errNotTLS
		}
	}
}

// serverName finds the server_name extension in the body of a ClientHello; empty when there is none or the message
// is not well formed (the TLS stack that gets it will say so).
func serverName(b []byte) string {
	skip := func(n int) bool {
		if n < 0 || n > len(b) {
			return false
		}
		b = b[n:]
		return true
	}
	// version, random
	if !skip(2 + 32) {
		return ""
	}
	// session id, cipher suites, compression methods
	for _, width := range []int{1, 2, 1} {
		if len(b) < width {
			return ""
		}
		n := int(b[0])
		if width == 2 {
			n = int(binary.BigEndian.Uint16(b))
		}
		if !skip(width + n) {
			return ""
		}
	}
	if len(b) < 2 {
		return ""
	}
	extLen := int(binary.BigEndian.Uint16(b))
	b = b[2:]
	if extLen > len(b) {
		extLen = len(b)
	}
	b = b[:extLen]
	for len(b) >= 4 {
		typ, n := binary.BigEndian.Uint16(b), int(binary.BigEndian.Uint16(b[2:]))
		b = b[4:]
		if n > len(b) {
			return ""
		}
		if typ != 0 { // server_name
			b = b[n:]
			continue
		}
		list := b[:n]
		if len(list) < 2 {
			return ""
		}
		list = list[2:]
		for len(list) >= 3 {
			nameType, nameLen := list[0], int(binary.BigEndian.Uint16(list[1:]))
			list = list[3:]
			if nameLen > len(list) {
				return ""
			}
			if nameType == 0 {
				return strings.ToLower(strings.TrimSuffix(string(list[:nameLen]), "."))
			}
			list = list[nameLen:]
		}
		return ""
	}
	return ""
}
