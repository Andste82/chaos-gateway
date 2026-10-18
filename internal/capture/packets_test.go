package capture

import (
	"encoding/binary"
	"net"
	"net/netip"
)

// pk describes a packet the tests build: Ethernet and IP headers, a transport header and a payload.
type pk struct {
	eth      bool   // with an Ethernet header
	src, dst string // Ethernet addresses
	sa, da   string // IP addresses
	proto    uint8  // protoTCP, protoUDP, protoICMP, protoICM6
	sp, dp   uint16
	payload  int
	frag     uint16 // fragment offset in 8-byte units
	options  int    // IPv4 option bytes (a multiple of 4)
}

// build returns the bytes of the packet.
func (p pk) build() []byte {
	sa, da := netip.MustParseAddr(p.sa), netip.MustParseAddr(p.da)
	var l4 []byte
	switch p.proto {
	case protoTCP:
		l4 = make([]byte, 20)
		binary.BigEndian.PutUint16(l4[0:], p.sp)
		binary.BigEndian.PutUint16(l4[2:], p.dp)
		l4[12] = 5 << 4
	case protoUDP:
		l4 = make([]byte, 8)
		binary.BigEndian.PutUint16(l4[0:], p.sp)
		binary.BigEndian.PutUint16(l4[2:], p.dp)
		binary.BigEndian.PutUint16(l4[4:], uint16(8+p.payload))
	default:
		l4 = make([]byte, 8)
	}
	l4 = append(l4, make([]byte, p.payload)...)
	var ip []byte
	if sa.Is4() {
		ihl := 20 + p.options
		ip = make([]byte, ihl)
		ip[0] = 0x40 | byte(ihl/4)
		binary.BigEndian.PutUint16(ip[2:], uint16(ihl+len(l4)))
		binary.BigEndian.PutUint16(ip[6:], p.frag)
		ip[8], ip[9] = 64, p.proto
		a, b := sa.As4(), da.As4()
		copy(ip[12:], a[:])
		copy(ip[16:], b[:])
	} else {
		ip = make([]byte, 40)
		ip[0] = 0x60
		binary.BigEndian.PutUint16(ip[4:], uint16(len(l4)))
		ip[6], ip[7] = p.proto, 64
		a, b := sa.As16(), da.As16()
		copy(ip[8:], a[:])
		copy(ip[24:], b[:])
	}
	out := append(ip, l4...)
	if p.eth {
		hdr := make([]byte, 14)
		d, _ := net.ParseMAC(p.dst)
		s, _ := net.ParseMAC(p.src)
		copy(hdr[0:], d)
		copy(hdr[6:], s)
		et := uint16(etherIPv4)
		if sa.Is6() {
			et = etherIPv6
		}
		binary.BigEndian.PutUint16(hdr[12:], et)
		out = append(hdr, out...)
	}
	return out
}

const (
	macDev   = "02:00:00:00:00:0b"
	macOther = "02:00:00:00:00:0c"
	macGW    = "02:00:00:00:00:01"
)

// tcpUp is a TCP packet from the device to a broker, tcpDown the answer.
func tcpUp(dport uint16) pk {
	return pk{eth: true, src: macDev, dst: macGW, sa: "10.10.0.11", da: "203.0.113.10", proto: protoTCP, sp: 40000, dp: dport, payload: 10}
}

func tcpDown(sport uint16) pk {
	return pk{eth: true, src: macGW, dst: macDev, sa: "203.0.113.10", da: "10.10.0.11", proto: protoTCP, sp: sport, dp: 40000, payload: 10}
}
