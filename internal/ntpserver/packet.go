package ntpserver

import (
	"encoding/binary"
	"errors"
	"time"
)

// This file is the NTP packet of RFC 5905 (and the SNTP subset of RFC 4330) as the server needs it: the 48-byte header
// without extension fields and MAC, which are neither read nor answered.

// PacketSize is the length of the NTP header, and so of every reply.
const PacketSize = 48

// Association modes (RFC 5905 §7.3).
const (
	ModeClient    = 3
	ModeServer    = 4
	ModeBroadcast = 5
	ModeControl   = 6 // ntpq: never answered
	ModePrivate   = 7 // ntpdc, monlist: never answered
)

// Leap indicators (RFC 5905 §7.3).
const (
	LeapNone           = 0
	LeapInsert         = 1 // the last minute of the day has 61 seconds
	LeapDelete         = 2 // the last minute of the day has 59 seconds
	LeapUnsynchronized = 3
)

// The extension fields of NTS (RFC 8915 §5.7): a client that has done the key establishment puts the first two into every
// query and the last into the MAC; the server cannot answer them.
const (
	extNTSUniqueIdentifier  = 0x0104
	extNTSCookie            = 0x0204
	extNTSCookiePlaceholder = 0x0304
	extNTSAuthenticator     = 0x0404
)

// hasNTSExtension reports whether a query carries an extension field of NTS after its header. The fields are a type and a
// length of two bytes each and a body padded to four bytes (RFC 7822); a field that does not fit ends the search.
func hasNTSExtension(b []byte) bool {
	for off := PacketSize; off+4 <= len(b); {
		typ, n := binary.BigEndian.Uint16(b[off:]), int(binary.BigEndian.Uint16(b[off+2:]))
		switch typ {
		case extNTSUniqueIdentifier, extNTSCookie, extNTSCookiePlaceholder, extNTSAuthenticator:
			return true
		}
		if n < 4 || n%4 != 0 || off+n > len(b) {
			return false
		}
		off += n
	}
	return false
}

// ntpToUnix is the span from the NTP epoch (1900-01-01) to the Unix epoch in seconds.
const ntpToUnix = 2208988800

// eraSeconds is the length of an NTP era: the seconds field has 32 bits.
const eraSeconds = int64(1) << 32

// Timestamp is the 64-bit NTP timestamp: seconds since 1900-01-01 in the upper and the fraction of a second in the
// lower 32 bits. The seconds wrap at the end of each era of 2^32 seconds (the first ends 2036-02-07 06:28:16 UTC), and
// a timestamp of 0 means "not set" (RFC 5905 §6).
type Timestamp uint64

// TimestampOf converts an instant. The seconds count modulo the era, which is how the protocol carries a time after the
// end of era 0 (a client resolves the era from its own clock). A time before 1900-01-01 00:00:01 cannot be carried: it
// is clamped to that second, because wrapping it would show a date in 2036 or later, and 0 means "not set".
func TimestampOf(t time.Time) Timestamp {
	sec := t.Unix() + ntpToUnix
	if sec < 1 {
		return Timestamp(1) << 32
	}
	// (nanoseconds << 32) / 1e9 fits: nanoseconds < 2^30
	frac := uint64(t.Nanosecond()) << 32 / 1_000_000_000
	return Timestamp(uint64(uint32(sec))<<32 | frac)
}

// Seconds and Fraction are the two halves.
func (ts Timestamp) Seconds() uint32  { return uint32(ts >> 32) }
func (ts Timestamp) Fraction() uint32 { return uint32(ts) }

// Time resolves the timestamp to the instant that lies within about 68 years of pivot (RFC 5905 §6, "era
// determination"): the clock of the reader is the pivot. A timestamp of 0 is not a time and gives the zero time.
func (ts Timestamp) Time(pivot time.Time) time.Time {
	if ts == 0 {
		return time.Time{}
	}
	sec := int64(ts.Seconds())
	base := pivot.Unix() + ntpToUnix
	// the era n for which sec + n*2^32 is closest to base: floor((base + 2^31 - sec) / 2^32)
	n := floorDiv(base+eraSeconds/2-sec, eraSeconds)
	nsec := int64((uint64(ts.Fraction())*1_000_000_000 + 1<<31) >> 32)
	return time.Unix(sec+n*eraSeconds-ntpToUnix, nsec).UTC()
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// Packet is an NTP header.
type Packet struct {
	Leap      uint8
	Version   uint8
	Mode      uint8
	Stratum   uint8
	Poll      int8
	Precision int8
	// RootDelay and RootDispersion are NTP short format: 16 bits of seconds and 16 of fraction
	RootDelay      uint32
	RootDispersion uint32
	// RefID is a kiss code at stratum 0, the IPv4 address of the source from stratum 2
	RefID     [4]byte
	Reference Timestamp
	Origin    Timestamp
	Receive   Timestamp
	Transmit  Timestamp
}

// ErrShort is the error of a datagram shorter than the header.
var ErrShort = errors.New("an NTP packet has at least 48 bytes")

// Parse reads the header of a datagram. Extension fields and a MAC after it are ignored. Any 48 bytes parse: what a
// packet means is for the server to decide.
func Parse(b []byte) (Packet, error) {
	if len(b) < PacketSize {
		return Packet{}, ErrShort
	}
	var p Packet
	p.Leap = b[0] >> 6
	p.Version = b[0] >> 3 & 7
	p.Mode = b[0] & 7
	p.Stratum = b[1]
	p.Poll = int8(b[2])
	p.Precision = int8(b[3])
	p.RootDelay = binary.BigEndian.Uint32(b[4:])
	p.RootDispersion = binary.BigEndian.Uint32(b[8:])
	copy(p.RefID[:], b[12:16])
	p.Reference = Timestamp(binary.BigEndian.Uint64(b[16:]))
	p.Origin = Timestamp(binary.BigEndian.Uint64(b[24:]))
	p.Receive = Timestamp(binary.BigEndian.Uint64(b[32:]))
	p.Transmit = Timestamp(binary.BigEndian.Uint64(b[40:]))
	return p, nil
}

// Marshal writes the header. A field wider than its place (a version above 7) is cut.
func (p Packet) Marshal() []byte {
	b := make([]byte, PacketSize)
	p.put(b)
	return b
}

func (p Packet) put(b []byte) {
	b[0] = p.Leap&3<<6 | p.Version&7<<3 | p.Mode&7
	b[1] = p.Stratum
	b[2] = byte(p.Poll)
	b[3] = byte(p.Precision)
	binary.BigEndian.PutUint32(b[4:], p.RootDelay)
	binary.BigEndian.PutUint32(b[8:], p.RootDispersion)
	copy(b[12:16], p.RefID[:])
	binary.BigEndian.PutUint64(b[16:], uint64(p.Reference))
	binary.BigEndian.PutUint64(b[24:], uint64(p.Origin))
	binary.BigEndian.PutUint64(b[32:], uint64(p.Receive))
	binary.BigEndian.PutUint64(b[40:], uint64(p.Transmit))
}

// shortFormat converts a duration to the 16.16 fixed point seconds of the root delay and dispersion, saturating.
func shortFormat(d time.Duration) uint32 {
	if d <= 0 {
		return 0
	}
	if d >= 65536*time.Second {
		return 0xffffffff
	}
	return uint32(uint64(d) << 16 / uint64(time.Second))
}
